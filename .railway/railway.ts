import { readFileSync } from "node:fs";
import { defineRailway, github, image, postgres, project, service, volume } from "railway/iac";

const repository = "patrickmonster/knowledge-base";
const ollamaScript = readFileSync(new URL("./ollama-start.sh", import.meta.url), "utf8");
const ollamaStart = "/bin/sh -c '" + ollamaScript.replaceAll("'", "'\\''") + "'";

export default defineRailway((ctx) => {
  if (ctx.environment !== "development" && ctx.environment !== "production") {
    throw new Error("Select development or production explicitly before applying.");
  }

  // Evaluated separately in each environment: credentials and data are isolated.
  const development = ctx.environment === "development";
  // Match the independently provisioned services in the existing project.
  const db = postgres(development ? "Postgres-m91O" : "Postgres");
  const models = volume(development ? "ollama-models-dev" : "ollama-models", { sizeMB: 4096 });
  const ollamaName = development ? "Ollama-QdV8" : "Ollama";
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
  const app = service(development ? "knowledge-base-dev" : "knowledge-base", {
    ...(repository ? {
      source: github(repository, {
        branch: ctx.environment === "production" ? "main" : "develop",
        checkSuites: true,
      }),
    } : {}),
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

  return project("knowledge-base", { resources: [db, models, ollama, app] });
});
