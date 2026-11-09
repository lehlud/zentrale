import type {
  Machine,
  ModelDefaults,
  Pool,
  Project,
  Provider,
  ProviderKind,
  SSHKey,
  SandboxStatus,
  CommitIdentity,
} from "./generated";

export type Config = {
  sandboxStatuses: SandboxStatus[];
  machines: Machine[];
  pools: Pool[];
  providers: Provider[];
  sshKeys: SSHKey[];
  trustedKeys: SSHKey[];
  projects: Project[];
  modelDefaults: ModelDefaults;
  providerKinds: ProviderKind[];
  commitIdentity: CommitIdentity;
};

export { Keys } from "./keys";
export { Machines } from "./machines";
export { Providers } from "./providers";
export { Settings } from "./settings";
