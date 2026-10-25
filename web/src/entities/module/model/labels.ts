import type { ModuleStatus, ValidationCheck } from "./schema";

export const moduleStatusLabels: Record<ModuleStatus, string> = {
  uploaded: "Загружен",
  validating: "Проверяется",
  validated: "Проверка пройдена",
  rejected: "Отклонён",
  archived: "В архиве",
};

type CheckFailure = { title: string; hint: string };

const checkFailures: Record<ValidationCheck["name"], CheckFailure> = {
  fetch: {
    title: "файл не удалось получить",
    hint: "Платформа не смогла прочитать загруженный файл. Загрузите его ещё раз.",
  },
  compile: {
    title: "модуль не компилируется",
    hint: "Проверьте, что это скомпилированный WebAssembly-модуль, пересоберите .wasm и загрузите снова.",
  },
  exports: {
    title: "нет функции обработчика",
    hint: "Модуль должен экспортировать функцию обработчика хука. Пересоберите .wasm и загрузите снова.",
  },
  imports: {
    title: "запрещённая возможность",
    hint: "Уберите вызов, который хук не разрешает, пересоберите .wasm и загрузите снова.",
  },
  sample_call: {
    title: "пробный вызов не прошёл",
    hint: "На примере входа обработчик упал или вернул ответ не по схеме выхода. Исправьте и загрузите снова.",
  },
};

export function describeCheckFailure(check: ValidationCheck): CheckFailure {
  return checkFailures[check.name];
}
