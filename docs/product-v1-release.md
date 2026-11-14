# Ритм: подготовка релиза web, Telegram и iPhone

Этап 22.10.2026. После ответа владельца граница выпуска — локально проверенные артефакты и инструкция. Публичный VPS не обновлён: доступ будет предоставлен позже. Apple Developer и контакт поддержки также отложены владельцем. Нативный iOS target пока не собран с iOS SDK.

## Что изменилось

- Web сохраняет последний открытый снимок при сбое обновления и помечает его устаревшим. Повторное чтение явно запускает пользователь. Ненастроенный AI ведёт к работающему помощнику по правилам. Есть встроенная помощь и подсказка сохранения гостевого доступа.
- В настройках показаны числа записей и серверные лимиты. Полный архив недель больше не блокирует чтение и экспорт при смене недели; старые snapshots и revision сохраняются. Дополнительная регрессия обнаружила тот же отказ при повторном входе, поэтому политика чтения распространена на обычную и Telegram-авторизацию. Новые записи сверх лимита отклоняются. Ошибки хранилища не подменяются успехом; cookie выдаётся только после подтверждённого чтения.
- Установка сайта на главный экран имеет manifest и PNG-иконки. Worker хранит только общую offline-страницу и SVG-значок, API и личный дневник не кэширует. Автономного сохранения нет.
- «Очистить дневник» честно описывает сохранение аккаунта. Отдельное «Удалить аккаунт» требует пароль, подтверждение и просмотренную revision; PostgreSQL атомарно удаляет credentials, дневник и все связанные сеансы. Старые гостевые запросы не могут воскресить дневник.
- Telegram получил отдельный Go runtime с личными /start, /help, /privacy, web_app-кнопкой, context/таймаутами, ограничением ответа и безопасными ошибками. Живой Bot API подтвердил меню и команды @gymbro_ai; polling на VPS ещё не запущен.
- iPhone получил Xcode-проект и нативные Сегодня, План, Дневник, вход, экспорт и удаление аккаунта. Расширенные функции открываются в HTTPS WKWebView с синхронизацией cookie. Foundation API-клиент запрещает redirects и соблюдает revision/binding.

## Почему такой подход

Сохранены текущая архитектура и контракты: Go/domain не зависит от Telegram или SwiftUI, frontend получает только производные limits, SQL-схема не меняется. Нет новых Go/JS-зависимостей, больших рефакторингов или автоматического изменения планов. Бот — отдельный синхронный процесс с одним владельцем lifetime. Ручной composition root сохранён; DI framework, брокер и новые фоновые goroutine — N/A.

Удаление аккаунта добавлено как отдельная операция для подготовки App Store; очистка дневника сохраняет совместимость. Файловый adapter возвращает 503 до изменений, потому что его прежний layout не обеспечивает общую транзакцию ledger+diary. Рабочий сервер использует PostgreSQL.

## Независимое ревью

ECC использован для capability scope, распределения владельцев, production audit и независимого ревью. Работа выполнялась волнами Sol/Astra при доступных четырёх слотах. [План](product-v1-plan.md), [доска](product-v1-board.json). Каталог не содержит Git; коммиты, rebase, cherry-pick и другие commit objects не создавались.

Исправлены все конкретные находки ревью:

1. Старый guest INSERT мог пройти первую проверку до регистрации и восстановить diary после удаления аккаунта. Повторная проверка tombstone выполняется под блокировкой строки; SQL-trigger regression детерминированно воспроизводит исходный порядок.
2. Подтверждение iPhone могло использовать обновлённую revision вместо просмотренной. Теперь сохраняется снимок, проверяется identity, новая запись даёт 409; добавлены две Swift-регрессии.
3. QA fingerprint не охватывал неизвестные расширения Go embed-ресурсов. Теперь весь допустимый static-каталог хешируется независимо от расширения и имени вложенного каталога.
4. Dangling symlink output мог перенаправить создание bundle. Исходный путь теперь проверяется до разрешения, тест подтверждает сохранность ссылки и её цели.
5. Повторный вход при полном архиве выдавал 413 даже после исправления GET/export. Общая политика calendarRecord теперь покрывает state, login и Telegram. Регрессии проверяют истёкшую cookie и повторный вход тремя способами через 60 дней, сохранность истории, экспорт и ошибки без новой cookie.

## Существенные ограничения

- Полный календарный архив читается и экспортируется, но восстановление в новую дату по-прежнему должно пройти лимиты, включая недостающие недели. Автоматического удаления/архивирования нет.
- Непрозрачные случайные identity IDs и created_at остаются как технические tombstones. В них нет логина, hash пароля, recovery digest или дневника. Ранее созданные SQL backup удаляются по ротации; эта операция не переписывает backups.
- Бот не делает рассылок. In-memory offset допускает повтор последнего ответа при crash до следующего getUpdates; exactly-once не заявляется. Webhook чужого consumer автоматически не удаляется.
- Реальные Telegram iPhone/Android/Web, новый runtime на VPS и публичный токен web-сервиса ещё требуют rollout/smoke после доступа. Public menu verification этого не заменяет.
- Полный Xcode/iOS SDK, simulator/device, signing, TestFlight и App Store validation не проверены. Foundation tests и syntax parse не доказывают iOS-сборку.
- /privacy.html и /support.html подготовлены, но контакт и сведения владельца отложены. Эти страницы не выдаются за финальные условия публичного выпуска.
- Off-host backup и канал аварийных уведомлений не настроены: destinations не предоставлены. Исторический VPS не имеет доказанного SLO для 10 тысяч пользователей.

## Артефакты и установка

[Пошаговое обновление и откат](release-v1-deployment.md), [воспроизводимый QA и builder](release-operations.md), [Telegram](telegram-product.md), [iPhone](ios-release.md). Пакеты находятся в dist/. В них нет предоставленных ключей AI и токена Telegram; проверены также распакованные данные архивов. Повторная сборка из текущих исходников воспроизвела все 14 Linux-файлов byte-for-byte. iOS-архив содержит 16 исходных/ресурсных файлов и внутренний SHA-256 manifest; архив и рабочие файлы сверены.

## Фактическая проверка — PASS локального релиза

Полный итог: [result.json](evidence/product-v1/release-qa/result.json). Перед и после QA совпали SHA-256 всех 196 покрытых файлов; fingerprint `6d66c318bd6598e40a12a42ae51b7fa980f64d5104f77500cab7c5d037019421` совпадает с manifest пакета.

| Проверка | Результат | Evidence / граница |
|---|---|---|
| `go test -race -count=1 ./...` | PASS | [Go](evidence/product-v1/release-qa/go-race.log); реальная отдельная PostgreSQL 16, включая rollback/конкурентность/истёкшие cookie |
| `go vet ./...`, `staticcheck ./...`, `gofmt -l cmd internal` | PASS | [vet](evidence/product-v1/release-qa/go-vet.log), [staticcheck](evidence/product-v1/release-qa/staticcheck.log); gofmt без вывода |
| `npm test` | 109/109 | [Node](evidence/product-v1/release-qa/frontend-tests.log) |
| `npm run check` | PASS | [Синтаксис production JS](evidence/product-v1/release-qa/frontend-check.log) |
| Python unittest discovery | 26/26 | [Python](evidence/product-v1/release-qa/python-tests.log), включая безопасный builder/QA и redaction setup |
| RAT | PASS | [Синтетические сценарии](evidence/product-v1/release-qa/rat.log), не внешний нагрузочный SLO |
| Chrome / Go / PostgreSQL | 9/9 suites | base, full product, AI fixtures, wellness/nutrition, accounts, first run, server day, consistency, v1 |
| Foundation core Swift | 13/13 | [Swift](evidence/product-v1/swift-core.log), [scope/source](evidence/product-v1/swift-result.json); macOS URLProtocol fixtures |
| SwiftUI syntax / plist / project structure | PASS | `swiftc -frontend -parse`, `plutil -lint`; не iOS typecheck |
| Linux amd64 bundle | PASS | 14 файлов, 13 внутренних SHA-256; повторная сборка byte-identical |
| Живое меню Telegram | PASS | [Bot API metadata](evidence/product-v1/telegram-configuration.json); без проверки polling/клиентского входа |
| iOS SDK / signing / App Store | Не выполнено | Полный Xcode/Apple Developer отсутствуют; владелец выбрал подготовку исходников |
| Публичный rollout | Отложен владельцем | SSH-доступ и контакты будут предоставлены позже |

Первоначальные прогоны сохранили строгие ожидания и выявили несовместимые старые UI selectors/неполный AI fixture. Селекторы уточнены по диалогу и доступному имени; fixture теперь одинаково задаёт синтетическую AI capability в state и настоящих ответах сохранения. Assertions сохранения, согласия, изоляции и отказов не ослаблялись. Последний общий прогон прошёл целиком на финальных исходниках. Более ранний PASS до дополнительной auth-регрессии хранится отдельно в `evidence/product-v1/pre-auth-capacity-qa/` и не подменяет последний результат.

Выполненная команда (при повторе нужен новый evidence-каталог):

```sh
GOCACHE=/private/tmp/rhythm-product-go-cache \
GOMODCACHE=/tmp/ritm-audit-modcache \
STATICCHECK_CACHE=/private/tmp/rhythm-product-staticcheck \
GOPROXY=off PATH=/Users/alexlepekhov/go/bin:$PATH \
python3 scripts/qa.py --evidence-dir /tmp/rhythm-v1-release-qa-final-auth-20261022
```

Окружение: Go 1.25.2, Node 25.6.1, Python 3.14.5, PostgreSQL 16.9, Swift 6.1.2. Временная БД и принадлежащие runner процессы остановлены; [pg-stop](evidence/product-v1/release-qa/pg-stop.log). Рабочая `.local` БД и секреты не использовались в этом gate.

Скачать: [Linux](../dist/ritm-20261022-linux-amd64.tar.gz), [исходники iPhone](../dist/ritm-20261022-ios-source.zip), [SHA256SUMS](../dist/ritm-20261022-SHA256SUMS), [manifest](../dist/ritm-20261022-artifacts.json). [Установка и откат](release-v1-deployment.md).

