# Проверка трёх замечаний к Go data plane M3

Проверенная ревизия — `efc5bc4`. Команды ниже предназначены для PowerShell 7 и запускаются из корня репозитория. Go overlay подключает три дополнительных теста к существующим пакетам на время запуска.

```powershell
$proofDir = (Resolve-Path 'docs/reviews/m3-audit').Path
$moduleDir = (Resolve-Path 'dataplane').Path
$overlayPath = Join-Path ([IO.Path]::GetTempPath()) ('m3-review-' + [guid]::NewGuid().ToString('N') + '.json')
$replace = @{}
$replace[(Join-Path $moduleDir 'internal/cluster/review_slot_test.go')] = Join-Path $proofDir 'slot-retry.go.txt'
$replace[(Join-Path $moduleDir 'internal/cluster/review_ring_test.go')] = Join-Path $proofDir 'ring-diagnostics.go.txt'
$replace[(Join-Path $moduleDir 'internal/executor/review_caller_test.go')] = Join-Path $proofDir 'caller-breaker.go.txt'
@{ Replace = $replace } | ConvertTo-Json | Set-Content -LiteralPath $overlayPath -Encoding utf8NoBOM
go -C $moduleDir test -count=1 -overlay $overlayPath -run '^TestReview' -v ./internal/cluster ./internal/executor
```

На указанной ревизии ожидаются три упавших проверки и код выхода 1.

| Тест | Что воспроизводит |
|---|---|
| `TestReviewReservedGuestBudget` | Два локальных gRPC-исполнителя. Первый возвращает подтверждённый отказ `deadline_before_start` после 45 мс из бюджета 150 мс. Второй готов вернуть `ok`. Тест проверяет, что запрос до него дошёл. Отдельный штатный тест `TestExecutorSlotWaitKeepsScriptBudget` покрывает выдачу такого отказа настоящим исполнителем при занятом слоте. |
| `TestReviewCallerBeforeBreaker` | Настоящие Gateway и Executor в combined-режиме. Тестовая обёртка гостевого вызова инициирует отмену и возвращается сразу после закрытия `ctx.Done()`; `GOMAXPROCS=1` воспроизводит порядок, когда обработчик отмены ещё не успел записать время. Тест проверяет результат первого вызова и состояние breaker при следующем запросе. |
| `TestReviewObservingMembershipDuringChange` | Последовательность чтений из обработчика диагностики с принудительным обновлением discovery между чтением участников и чтением владельцев. Тест сравнивает участников с полным списком владельцев одной пары. |
