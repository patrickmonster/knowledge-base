package kb

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"path"
	"strings"
	"time"
)

// MalwareScanner inspects an uploaded file before it reaches the bucket.
// A non-empty signature means the file is infected and must be rejected.
type MalwareScanner interface {
	Scan(ctx context.Context, r io.Reader) (signature string, err error)
	Name() string
	// Ping checks that the scanner is reachable without scanning a file.
	Ping(ctx context.Context) error
}

// ClamAV talks to clamd over TCP with the INSTREAM command. clamd's
// StreamMaxLength must be at least MaxAttachmentBytes (25 MiB), or large files
// fail closed with a scan error.
type ClamAV struct {
	Address string
	Timeout time.Duration
}

func (c ClamAV) Name() string { return "clamav" }

// Ping sends zPING and expects PONG. clamd answers only once its signature
// database is loaded, so this also reports the first-start download window.
func (c ClamAV) Ping(ctx context.Context) error {
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", c.Address)
	if err != nil {
		return fmt.Errorf("clamd connect: %w", err)
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if _, err = conn.Write([]byte("zPING\x00")); err != nil {
		return err
	}
	reply, err := io.ReadAll(io.LimitReader(conn, 64))
	if err != nil {
		return err
	}
	if string(bytes.TrimRight(reply, "\x00\n")) != "PONG" {
		return errors.New("clamd ping: unexpected reply")
	}
	return nil
}

func (c ClamAV) Scan(ctx context.Context, r io.Reader) (string, error) {
	timeout := c.Timeout
	if timeout == 0 {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", c.Address)
	if err != nil {
		return "", fmt.Errorf("clamd connect: %w", err)
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if _, err = conn.Write([]byte("zINSTREAM\x00")); err != nil {
		return "", err
	}
	buf := make([]byte, 64<<10)
	size := make([]byte, 4)
	for {
		n, readErr := r.Read(buf)
		if n > 0 {
			binary.BigEndian.PutUint32(size, uint32(n))
			if _, err = conn.Write(size); err != nil {
				return "", err
			}
			if _, err = conn.Write(buf[:n]); err != nil {
				return "", err
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return "", readErr
		}
	}
	if _, err = conn.Write([]byte{0, 0, 0, 0}); err != nil {
		return "", err
	}
	reply, err := io.ReadAll(io.LimitReader(conn, 4096))
	if err != nil {
		return "", err
	}
	return parseClamReply(string(bytes.TrimRight(reply, "\x00\n")))
}

// parseClamReply maps "stream: OK" to clean, "stream: <name> FOUND" to a
// signature, and anything else (size limit, ERROR) to an error.
func parseClamReply(reply string) (string, error) {
	reply = strings.TrimPrefix(reply, "stream: ")
	switch {
	case reply == "OK":
		return "", nil
	case strings.HasSuffix(reply, " FOUND"):
		sig := strings.TrimSpace(strings.TrimSuffix(reply, " FOUND"))
		if sig == "" || len(sig) > 200 {
			sig = "unknown"
		}
		return sig, nil
	default:
		return "", errors.New("clamd scan error")
	}
}

// blockedExtensions are directly executable on common desktops. They are
// refused even without an antivirus service; knowledge files never need them.
var blockedExtensions = map[string]bool{
	".exe": true, ".dll": true, ".scr": true, ".com": true, ".msi": true, ".msp": true, ".bat": true, ".cmd": true,
	".ps1": true, ".psm1": true, ".vbs": true, ".vbe": true, ".jse": true, ".wsf": true, ".wsh": true, ".hta": true,
	".lnk": true, ".pif": true, ".cpl": true, ".jar": true, ".apk": true, ".appx": true, ".msix": true, ".reg": true,
}

func blockedFilename(name string) bool {
	return blockedExtensions[strings.ToLower(path.Ext(strings.TrimRight(name, ". ")))]
}

// executableMagic detects native executables regardless of their file name.
func executableMagic(head []byte) bool {
	switch {
	case bytes.HasPrefix(head, []byte("MZ")) && len(head) >= 0x40: // Windows PE: MZ stub pointing at a PE header
		offset := int(binary.LittleEndian.Uint32(head[0x3c:0x40]))
		return offset >= 0x40 && offset+4 <= len(head) && bytes.Equal(head[offset:offset+4], []byte{'P', 'E', 0, 0})
	case bytes.HasPrefix(head, []byte("\x7fELF")): // Linux
		return true
	case bytes.HasPrefix(head, []byte{0xcf, 0xfa, 0xed, 0xfe}), bytes.HasPrefix(head, []byte{0xce, 0xfa, 0xed, 0xfe}),
		bytes.HasPrefix(head, []byte{0xfe, 0xed, 0xfa, 0xcf}), bytes.HasPrefix(head, []byte{0xfe, 0xed, 0xfa, 0xce}): // Mach-O
		return true
	}
	return false
}
