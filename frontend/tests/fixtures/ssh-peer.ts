import { spawn } from "node:child_process";
import { fileURLToPath } from "node:url";

export type SSHPeer = {
  host: string;
  port: number;
  publicKey: string;
  close: () => Promise<void>;
};

export async function startSSHPeer(): Promise<SSHPeer> {
  const process = spawn("go", ["run", "./cmd/testssh"], {
    cwd: fileURLToPath(new URL("../../../backend/", import.meta.url)),
    stdio: "pipe",
  });
  let errors = "";
  process.stderr.on("data", (chunk) => {
    errors = (errors + String(chunk)).slice(-16000);
  });
  const details = await new Promise<Omit<SSHPeer, "close">>(
    (resolve, reject) => {
      let output = "";
      const timeout = setTimeout(() => {
        process.stdin.end();
        reject(new Error(`SSH fixture did not start: ${errors}`));
      }, 30000);
      process.once("error", (error) => {
        clearTimeout(timeout);
        reject(error);
      });
      process.once("exit", () => {
        clearTimeout(timeout);
        reject(new Error(`SSH fixture stopped: ${errors}`));
      });
      process.stdout.on("data", (chunk) => {
        output += String(chunk);
        if (!output.includes("\n")) {
          return;
        }

        clearTimeout(timeout);

        try {
          resolve(JSON.parse(output.split("\n")[0]));
        } catch (error) {
          reject(error);
        }
      });
    },
  );

  return {
    ...details,
    close: () =>
      new Promise<void>((resolve) => {
        if (process.exitCode != null) {
          resolve();
          return;
        }

        const timeout = setTimeout(() => {
          process.kill();
          resolve();
        }, 10000);
        process.once("exit", () => {
          clearTimeout(timeout);
          resolve();
        });
        process.stdin.end();
      }),
  };
}
