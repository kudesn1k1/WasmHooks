import { MAX_MODULE_BYTES } from "@/entities/module";

const WASM_MAGIC = [0x00, 0x61, 0x73, 0x6d];

export async function checkWasmFile(file: File): Promise<string | null> {
  if (file.size === 0) return "Файл пустой";
  if (file.size > MAX_MODULE_BYTES) return "Файл больше 10 МиБ: платформа такой модуль не примет";
  const head = new Uint8Array(await file.slice(0, 4).arrayBuffer());
  if (!WASM_MAGIC.every((byte, i) => head[i] === byte)) {
    return "Это не WebAssembly-модуль: нужен скомпилированный файл .wasm";
  }
  return null;
}
