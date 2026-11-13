import { readFileSync } from "node:fs";
import { defineRailway, github, image, postgres, project, service, volume } from "railway/iac";

type DeploymentConfiguration = {
  projectName: string;
  repository: string;
  environments: Record<"development" | "production", {
    app: { name: string; branch: string };
    database: { name: string };
    ollama: { name: string; volumeName: string };
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
for (const environment of ["development", "production"] as const) {
  const resources = configuration.environments?.[environment];
  const required = [configuration.projectName, configuration.repository, resources?.app?.name,
    resources?.app?.branch, resources?.database?.name, resources?.ollama?.name,
    resources?.ollama?.volumeName];
  if (required.some((value) => typeof value !== "string" || !value.trim())) {
    throw new Error(`Incomplete local deployment configuration for ${environment}.`);
  }
}
const ollamaScript = readFileSync(new URL("./ollama-start.sh", import.meta.url), "utf8");
const ollamaStart = "/bin/sh -c '" + ollamaScript.replaceAll("'", "'\\''") + "'";

export default defineRailway((ctx) => {
  if (ctx.environment !== "development" && ctx.environment !== "production") {
    throw new Error("Select development or production explicitly before applying.");
  }

  // Evaluated separately in each environment: credentials and data are isolated.
  const resources = configuration.environments[ctx.environment];
  // Match the independently provisioned services in the existing project.
  const db = postgres(resources.database.name);
  const models = volume(resources.ollama.volumeName, { sizeMB: 4096 });
  const ollamaName = resources.ollama.name;
  const ollama = service(ollamaName, {
    source: image("ollama/ollama:0.40.16"),
    volumeMounts: { "/root/.ollama": models },
    env: {
      OLLAMA_HOST: "[::]:11434",
      OLLAMA_NO_CLOUD: "true",
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
      APP_ENV: ctx.environment,
      DATABASE_URL: db.env.DATABASE_URL,
      PUBLIC_URL: "https://${{RAILWAY_PUBLIC_DOMAIN}}",
      PORT: "8080",
      TRUST_PROXY: "true",
      OLLAMA_BASE_URL: "http://${{" + ollamaName + ".RAILWAY_PRIVATE_DOMAIN}}:11434",
      EMBEDDING_MODEL: "bge-m3",
      EMBEDDING_ENABLED: "true",
    },
  });

  return project(configuration.projectName, { resources: [db, models, ollama, app] });
});
