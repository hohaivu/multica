import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, describe, it, expect } from "vitest";
import { chatProjectContextSupported } from "@multica/core/runtimes";
import { devCliVersionFromMakefile } from "./bundle-cli.mjs";

let dir;

afterEach(() => {
  if (dir) rmSync(dir, { recursive: true, force: true });
  dir = undefined;
});

describe("devCliVersionFromMakefile", () => {
  it("reads the DEV_CLI_VERSION constant out of the Makefile", () => {
    dir = mkdtempSync(join(tmpdir(), "bundle-cli-test-"));
    const makefile = join(dir, "Makefile");
    writeFileSync(makefile, "DEV_CLI_VERSION := v0.4.30-dev\n");
    expect(devCliVersionFromMakefile(makefile)).toBe("v0.4.30-dev");
  });

  it("throws when the Makefile has no DEV_CLI_VERSION", () => {
    dir = mkdtempSync(join(tmpdir(), "bundle-cli-test-"));
    const makefile = join(dir, "Makefile");
    writeFileSync(makefile, "VERSION := whatever\n");
    expect(() => devCliVersionFromMakefile(makefile)).toThrow();
  });

  it("matches the real repo Makefile and clears the project-context gate", () => {
    const repoRoot = join(import.meta.dirname, "..", "..", "..");
    const version = devCliVersionFromMakefile(join(repoRoot, "Makefile"));
    expect(chatProjectContextSupported(version)).toBe(true);
  });
});
