package kb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"time"
)

// TextExtractor turns an uploaded file into text. Store.Extractor nil disables
// extraction.
type TextExtractor interface {
	Extract(ctx context.Context, format, file string) (ExtractResult, error)
}

// InProcessExtractor parses in the calling process. Tests use it; the server
// uses ProcessExtractor.
type InProcessExtractor struct{}

func (InProcessExtractor) Extract(ctx context.Context, format, file string) (ExtractResult, error) {
	type out struct {
		res ExtractResult
		err error
	}
	done := make(chan out, 1)
	go func() {
		res, err := ExtractText(format, file)
		done <- out{res, err}
	}()
	select {
	case <-ctx.Done():
		return ExtractResult{}, extractErr(ExtractTimeout)
	case o := <-done:
		return o.res, o.err
	}
}

// DefaultExtractTimeout leaves room for the bucket PUT inside the 120 second
// upload request limit.
const DefaultExtractTimeout = 45 * time.Second

// extractMemoryLimit is the child's soft Go heap limit.
const extractMemoryLimit = "384MiB"

// ProcessExtractor runs "<Executable> extract-text <format> <file>" so a parser
// crash, hang or runaway allocation ends in a killed child, not a stuck server.
// The child gets no environment from the server (no database URL, bucket keys
// or other secrets) and only the file path as input.
type ProcessExtractor struct {
	Executable string
	Timeout    time.Duration
}

type extractReply struct {
	ExtractResult
	Error string `json:"error,omitempty"`
}

func (p ProcessExtractor) Extract(ctx context.Context, format, file string) (ExtractResult, error) {
	timeout := p.Timeout
	if timeout <= 0 {
		timeout = DefaultExtractTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, p.Executable, "extract-text", format, file)
	cmd.Env = []string{"GOMEMLIMIT=" + extractMemoryLimit, "GOMAXPROCS=1"}
	if root := os.Getenv("SYSTEMROOT"); root != "" {
		cmd.Env = append(cmd.Env, "SYSTEMROOT="+root) // required by Windows processes
	}
	var stdout bytes.Buffer
	cmd.Stdout = &limitedWriter{w: &stdout, left: 8 * MaxExtractedRunes}
	cmd.Stderr = io.Discard
	err := cmd.Run()
	if ctx.Err() != nil {
		return ExtractResult{}, extractErr(ExtractTimeout)
	}
	if err != nil {
		return ExtractResult{}, extractErr(ExtractFailed)
	}
	var reply extractReply
	if err := json.Unmarshal(stdout.Bytes(), &reply); err != nil {
		return ExtractResult{}, extractErr(ExtractFailed)
	}
	switch reply.Error {
	case "":
	case ExtractInvalidFile, ExtractEncrypted, ExtractTooLarge:
		return ExtractResult{}, extractErr(reply.Error)
	default:
		return ExtractResult{}, extractErr(ExtractFailed)
	}
	return reply.ExtractResult, nil
}

type limitedWriter struct {
	w    io.Writer
	left int
}

var errOutputTooLarge = errors.New("extract output too large")

func (l *limitedWriter) Write(p []byte) (int, error) {
	if len(p) > l.left {
		return 0, errOutputTooLarge
	}
	l.left -= len(p)
	return l.w.Write(p)
}

// RunExtractTextCommand implements the "extract-text <format> <file>" child
// command: it writes one JSON reply to stdout. Parser errors are reported in the
// reply; a non-zero exit means the child itself failed.
func RunExtractTextCommand(args []string, stdout io.Writer) error {
	if len(args) != 2 {
		return errors.New("usage: extract-text <pdf|docx|xlsx> <file>")
	}
	res, err := ExtractText(args[0], args[1])
	reply := extractReply{ExtractResult: res}
	if err != nil {
		reply = extractReply{Error: ExtractCode(err)}
	}
	return json.NewEncoder(stdout).Encode(reply)
}
