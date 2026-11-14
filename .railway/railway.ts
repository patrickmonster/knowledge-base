import { readFileSync } from "node:fs";
import { bucket, defineRailway, github, image, postgres, project, service, volume } from "railway/iac";

type DeploymentConfiguration = {
  projectName: string;
  repository: string;
  environments: Record<string, {
    attachments?: { name: string; region?: "sjc" | "iad" | "ams" | "sin"; forcePathStyle?: boolean };
    app: { name: string; branch: string };
    database: { name: string };
    ollama: { name: string; volumeName: string };
    // Optional clamd service; when present every upload is scanned (fail closed).
    clamav?: { name: string; volumeName: string };
  }>;
};

// Keep live resource names and identifiers out of the public repository.
// Fail before planning changes when the local deployment configuration is absent.
const configurationPath = new URL("./deployment.local.json", import.meta.url);
let configuration: DeploymentConfiguration;
try {
  configuration = JSON.parse(readFileSync(configurationPath, "utf8"));
} catch {
  throw new Error("Create .railway/deployment.local.json from deployment.example.json and fill in your existing resources before planning or applying.");
}
for (const environment of Object.keys(configuration.environments ?? {})) {
  const resources = configuration.environments?.[environment];
  const required = [configuration.projectName, configuration.repository, resources?.app?.name,
    resources?.app?.branch, resources?.database?.name, resources?.ollama?.name,
    resources?.ollama?.volumeName];
  if (required.some((value) => typeof value !== "string" || !value.trim())) {
    throw new Error(`Incomplete local deployment configuration for ${environment}.`);
  }
  if (resources.attachments && (!resources.attachments.name?.trim() || (resources.attachments.region && !["sjc","iad","ams","sin"].includes(resources.attachments.region)) || (resources.attachments.forcePathStyle !== undefined && typeof resources.attachments.forcePathStyle !== "boolean"))) {
    throw new Error(`Invalid bucket configuration for ${environment}.`);
  }
  if (resources.clamav && [resources.clamav.name, resources.clamav.volumeName].some((value) => typeof value !== "string" || !value.trim())) {
    throw new Error(`Invalid ClamAV configuration for ${environment}.`);
  }
}
const ollamaScript = readFileSync(new URL("./ollama-start.sh", import.meta.url), "utf8");
const ollamaStart = "/bin/sh -c '" + ollamaScript.replaceAll("'", "'\\''") + "'";

export default defineRailway((ctx) => {
  if (!ctx.environment || !configuration.environments[ctx.environment] || ctx.environment === "development") {
    throw new Error("Select an explicitly configured environment. The retired development environment cannot be applied.");
  }

  // Evaluated separately in each environment: credentials and data are isolated.
  const resources = configuration.environments[ctx.environment];
  // Match the independently provisioned services in the existing project.
  const db = postgres(resources.database.name);
  const files = resources.attachments ? bucket(resources.attachments.name, {region:resources.attachments.region ?? "sjc"}) : undefined;
  const bucketRef = (key: string) => "${{" + files!.name + "." + key + "}}";
  const models = volume(resources.ollama.volumeName, { sizeMB: 4096 });
  const ollamaName = resources.ollama.name;
  const ollama = service(ollamaName, {
    source: image("ollama/ollama:0.40.16"),
    volumeMounts: { "/root/.ollama": models },
    env: {
      OLLAMA_HOST: "[::]:11434",
      OLLAMA_NO_CLOUD: "true",
      // One model and one request at a time; the app sets num_thread per request.
      OLLAMA_NUM_PARALLEL: "1",
      OLLAMA_MAX_LOADED_MODELS: "1",
      PORT: "11434",
    },
    deploy: {
      startCommand: ollamaStart,
      healthcheckPath: "/api/tags",
      healthcheckTimeout: 120,
      numReplicas: 1,
      sleepApplication: false,
      restartPolicyType: "ON_FAILURE",
      restartPolicyMaxRetries: 5,
    },
  });
  // clamd for upload malware scanning. The _base image has no signatures: the
  // first start downloads them into the volume before clamd listens, so uploads
  // return SCAN_UNAVAILABLE for a few minutes. freshclam then updates in place.
  const signatures = resources.clamav ? volume(resources.clamav.volumeName, { sizeMB: 1024 }) : undefined;
  const clamav = resources.clamav ? service(resources.clamav.name, {
    source: image("clamav/clamav:1.4_base"),
    volumeMounts: { "/var/lib/clamav": signatures! },
    env: {
      // The entrypoint maps CLAMD_CONF_<Key> / FRESHCLAM_CONF_<Key> onto the config files.
      // Listen on the private network (IPv6) instead of IPv4-only INADDR_ANY.
      CLAMD_CONF_TCPAddr: "::",
      // Uploads are at most 25 MiB; INSTREAM framing needs a little headroom.
      CLAMD_CONF_StreamMaxLength: "26M",
      // Reload signatures in place: about half the peak memory, scans pause briefly.
      CLAMD_CONF_ConcurrentDatabaseReload: "no",
      FRESHCLAM_CHECKS: "12",
      CLAMAV_NO_MILTERD: "true",
    },
    deploy: {
      numReplicas: 1,
      sleepApplication: false,
      restartPolicyType: "ON_FAILURE",
      restartPolicyMaxRetries: 5,
    },
  }) : undefined;
  const app = service(resources.app.name, {
    source: github(configuration.repository, {
      branch: resources.app.branch,
      checkSuites: true,
    }),
    build: { builder: "DOCKERFILE", dockerfilePath: "Dockerfile" },
    deploy: {
      startCommand: "/app/knowledge-base serve",
      preDeployCommand: ["/app/knowledge-base migrate"],
      preDeployTimeoutSeconds: 300,
      healthcheckPath: "/healthz",
      healthcheckTimeout: 60,
      numReplicas: 1,
      restartPolicyType: "ON_FAILURE",
      restartPolicyMaxRetries: 5,
      sleepApplication: false,
    },
    env: {
      APP_ENV: "production",
      DATABASE_URL: db.env.DATABASE_URL,
      PUBLIC_URL: "https://${{RAILWAY_PUBLIC_DOMAIN}}",
      PORT: "8080",
      TRUST_PROXY: "true",
      OLLAMA_BASE_URL: "http://${{" + ollamaName + ".RAILWAY_PRIVATE_DOMAIN}}:11434",
      EMBEDDING_MODEL: "bge-m3",
      EMBEDDING_NUM_THREADS: "4",
      EMBEDDING_ENABLED: "true",
      ...(clamav ? {CLAMAV_ADDRESS: "${{" + resources.clamav!.name + ".RAILWAY_PRIVATE_DOMAIN}}:3310"} : {}),
      ...(files ? {BUCKET_ENDPOINT: bucketRef("ENDPOINT"), BUCKET_NAME: bucketRef("BUCKET"), BUCKET_REGION: bucketRef("REGION"), BUCKET_ACCESS_KEY_ID: bucketRef("ACCESS_KEY_ID"), BUCKET_SECRET_ACCESS_KEY: bucketRef("SECRET_ACCESS_KEY"), BUCKET_FORCE_PATH_STYLE: String(resources.attachments?.forcePathStyle ?? false)} : {}),
    },
  });

  return project(configuration.projectName, { resources: [db, models, ollama, app, ...(files ? [files] : []), ...(clamav ? [signatures!, clamav] : [])] });
});
