import { describe, expect, it } from "vitest";

import { checkWasmFile } from "./check-wasm";

const file = (bytes: number[]) => new File([new Uint8Array(bytes)], "m.wasm");

describe("checkWasmFile", () => {
  it("accepts a module with the wasm magic", async () => {
    await expect(checkWasmFile(file([0x00, 0x61, 0x73, 0x6d, 0x01, 0, 0, 0]))).resolves.toBeNull();
  });

  it("rejects other files", async () => {
    await expect(checkWasmFile(file([0x7f, 0x45, 0x4c, 0x46]))).resolves.toMatch(/не WebAssembly/);
  });

  it("rejects an empty file", async () => {
    await expect(checkWasmFile(file([]))).resolves.toMatch(/пустой/);
  });
});
