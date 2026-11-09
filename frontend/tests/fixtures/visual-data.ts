export const gitConnections = ["github", "gitlab"].map((kind) => ({
  id: kind,
  kind,
  baseUrl: `https://${kind}.com`,
  clientId: "",
  configured: false,
  connected: false,
  userName: "",
}));

// Deliberately long, realistic content makes wrapping problems visible in the audit.
export const machine = {
  id: "training",
  name: "GPU-Worker Frankfurt",
  host: "training-01.compute.frankfurt.example.org",
  port: 22,
  user: "research",
  keyId: 1,
  jumps: [],
  properties: [
    "NVIDIA A100 80 GB",
    "CUDA 12",
    "256 GB RAM",
    "Checkpoint-Speicher",
  ],
  sandbox: "auto",
  singleUser: true,
  containers: false,
  microVMCommand: "",
};

export const project = {
  id: "forecast",
  name: "Modelltraining & Evaluation",
  directory: "/srv/projects/forecast/checkpoint-training",
  machineIds: [machine.id],
  poolIds: ["pool"],
  commitName: "",
  commitEmail: "",
};

const provider = {
  id: "openai",
  name: "OpenAI · Forschung",
  kind: "openai",
  baseUrl: "",
  authMode: "api",
  billingMode: "metered",
  connected: true,
  accountLabel: "",
  catalogStatus: "ready",
  catalogError: "",
  modelCount: 3,
};

export const models = [
  "Hauptmodell",
  "Schnelle Freigaben",
  "Recherche mit großem Kontext",
].map((name, index) => ({
  id: `research-language-model-${index + 1}`,
  name,
  providerId: provider.id,
  contextWindow: 128000,
  inputPrice: 1.5,
  outputPrice: 6,
  cachedPrice: 0.15,
  pricesKnown: true,
  metadataEstimated: false,
}));

export const config = {
  gitConnections,
  commitIdentity: { name: "Entwicklungsteam", email: "dev@example.org" },
  sandboxStatuses: [
    {
      machineId: machine.id,
      mode: "auto",
      level: "bwrap",
      checkedAt: "2026-11-03T13:45:00Z",
      error: "",
    },
  ],
  machines: [machine],
  projects: [project],
  pools: [
    {
      id: "pool",
      name: "Forschungspool",
      maxMachines: 1,
      sharedHome: false,
      workspaceRoot: "/srv/projects/forecast/checkpoint-training",
      singleUser: true,
      sandbox: "auto",
      machines: [machine],
    },
  ],
  providers: [
    provider,
    {
      ...provider,
      id: "chatgpt",
      name: "ChatGPT · Team",
      authMode: "device",
      billingMode: "fixed",
      connected: false,
      modelCount: 0,
    },
    {
      ...provider,
      id: "openrouter",
      name: "OpenRouter",
      kind: "openrouter",
      authMode: "browser",
      connected: false,
      modelCount: 0,
    },
  ],
  sshKeys: [
    {
      id: 1,
      purpose: "machine",
      purposeId: machine.id,
      canDelete: false,
      canRevoke: false,
      usages: [
        { kind: "machine", id: machine.id, name: machine.name, projectId: "" },
      ],
      publicKey:
        "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIMockPublicKeyForVisualReviewOnly zentrale",
      fingerprint: "SHA256:2HjvGf7GpmCq6cMB5x8WC6sZ4QFvHDn3WvYdZtE9Sk0",
    },
    {
      id: 2,
      publicKey: "ssh-ed25519 unused-visual-key",
      fingerprint: "SHA256:UnusedVisualKey",
      purpose: "",
      purposeId: "",
      canDelete: true,
      canRevoke: false,
      usages: [],
    },
  ],
  trustedKeys: [
    {
      publicKey: "ssh-ed25519 visual-review",
      fingerprint: "SHA256:7HjvGf7GpmCq6cMB5x8WC6sZ4QFvHDn3WvYdZtE9Sk0",
    },
  ],
  modelDefaults: {
    main: { providerId: provider.id, modelId: models[0].id },
    approver: { providerId: provider.id, modelId: models[1].id },
  },
  providerKinds: [
    { id: "openai", name: "OpenAI", authMethods: ["api", "device"] },
    { id: "anthropic", name: "Anthropic", authMethods: ["api"] },
    { id: "openrouter", name: "OpenRouter", authMethods: ["api", "browser"] },
    { id: "litellm", name: "LiteLLM", authMethods: ["api"] },
  ],
};

export const agent = {
  id: "main",
  name: "Trainingsplanung",
  role: "main",
  status: "idle",
  parentId: "",
  approverId: "approver",
  projectId: project.id,
  providerId: provider.id,
  modelId: models[0].id,
  machineId: machine.id,
  error: "",
  contextStart: 0,
  summary:
    "Training mit Checkpoints fortsetzen. Nach einem Maschinenwechsel wird der Agent informiert und startet den Prozess selbst neu.",
};

export const agents = [
  agent,
  {
    ...agent,
    id: "approver",
    name: "Auto-Approver",
    role: "approver",
    parentId: agent.id,
    approverId: "",
  },
  {
    ...agent,
    id: "research",
    name: "Recherche",
    role: "subagent",
    parentId: agent.id,
  },
];

const createdAt = "2026-11-03T13:45:00Z";

export const transcript = {
  agent,
  messages: [
    {
      id: "m1",
      agentId: agent.id,
      role: "user",
      content:
        "Prüfe den letzten Trainingslauf und bereite die Fortsetzung ab Checkpoint 42 vor.",
      wire: "",
      createdAt,
    },
    {
      id: "m2",
      agentId: agent.id,
      role: "assistant",
      content:
        "Der Checkpoint ist vorhanden. Ich habe die Parameter geprüft und eine Rückfrage zur nächsten Evaluation vorbereitet.",
      wire: JSON.stringify({
        reasoning: "Checkpoint und Trainingsparameter geprüft.",
        action: "note_save",
      }),
      createdAt,
    },
    {
      id: "m3",
      agentId: agent.id,
      role: "tool",
      toolName: "note_save",
      content: JSON.stringify({ saved: true, title: "Trainingsplan" }),
      wire: "",
      createdAt,
    },
  ],
  recentMessages: [],
  approvals: [
    {
      id: "a1",
      tool: "exec",
      arguments: JSON.stringify({
        command: "python train.py --resume checkpoints/epoch-42.pt",
      }),
      allowed: false,
      reason:
        "Der Neustart benötigt zuerst die bestätigten Trainingsparameter.",
      blanket: false,
      createdAt,
    },
    {
      id: "a2",
      tool: "read_file",
      arguments: JSON.stringify({ path: "config/training.yaml" }),
      allowed: true,
      reason: "Lesender Zugriff auf die Projektkonfiguration.",
      blanket: true,
      createdAt,
    },
  ],
  questions: [
    {
      id: "q1",
      agentId: agent.id,
      title: "Welche Evaluation soll nach dem nächsten Checkpoint laufen?",
      options: ["Vollständiger Validierungsdatensatz", "Schneller Testlauf"],
      status: "open",
      answer: "",
    },
  ],
  jobs: [
    {
      id: "j1",
      agentId: agent.id,
      machineId: machine.id,
      command: "python train.py --resume checkpoints/epoch-42.pt",
      status: "running",
      output:
        "Epoch 43/100\nloss=0.182 · checkpoint saved\nValidation pending…",
      error: "",
      startedAt: createdAt,
    },
    {
      id: "j2",
      agentId: agent.id,
      machineId: machine.id,
      command: "python evaluate.py --checkpoint checkpoints/epoch-41.pt",
      status: "failed",
      output: "Loading validation data…",
      error:
        "Die Verbindung wurde unterbrochen. Den letzten Checkpoint prüfen und den Job erneut starten.",
      startedAt: createdAt,
    },
  ],
  notes: [
    {
      id: "n1",
      agentId: agent.id,
      title: "Trainingsplan",
      content:
        "Checkpoints nach jeder Epoche sichern. Vor einem Neustart Speicher und verfügbare GPU prüfen.",
    },
  ],
  usageSummary: {
    inputTokens: 42500,
    outputTokens: 1800,
    cachedTokens: 32000,
    cacheInputTokens: 42500,
    cacheReadTokens: 32000,
    cacheWriteTokens: 6000,
    unknownTokenCounts: 0,
    unknownCacheWrites: 0,
    realCost: 0.0732,
    fictionalCost: 0.0732,
    estimated: true,
    unknownPrices: 0,
    requestCount: 6,
  },
};

export const sources = [
  {
    id: "source",
    projectId: project.id,
    name: "training",
    url: "git@github.com:research-team/forecast-training-and-evaluation.git",
    keyId: 1,
    readOnly: true,
  },
];
