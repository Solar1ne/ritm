# PostgreSQL и завершённый первый запуск

> Последующее подключение DeepSeek описано в [отдельном отчёте](deepseek.md). Ниже сохранены результаты предыдущего этапа.

17.10.2026. Локальный сайт на 127.0.0.1:8080 работает на настоящем PostgreSQL 16, а не на файловом adapter. Работа выполнена с ECC postgres-patterns, database-migrations, product-capability и make-interfaces-feel-better; дополнительный database review проверил TLS, перенос и конкурентность.

## Что изменилось

- Добавлены SQL-схема, pgx pool, транзакционные записи и scoped auth lookup. Дневник — ограниченный JSONB-агрегат; аккаунты/хеши сессий — отдельные таблицы. HTTP-контракт и правила планирования сохраняются.
- Добавлены явная команда миграции схемы, проверка checksum, офлайн-перенос с pending marker, read-back, сохранением signing key и безопасным повтором. Автоматического отката к старой файловой истории нет.
- Перенесены 89 дневников из 90 исходных файлов (включая один созданный нами migration fixture и файл ключа). Это число записей хранилища, не число реальных пользователей. До переноса зарегистрированных аккаунтов в source не было; сохранение credential/session проверено отдельными SQL и browser fixtures.
- Прежняя гостевая cookie открыла тот же профиль, план, revision и session binding после переноса и после рестарта приложения. Собственный migration fixture очищен; пользовательские файлы сохранены без изменения.
- SQL dump восстановлен в отдельную тестовую БД. Сравнены все 8 таблиц, включая diaries, identities, account/session digests, ключ и metadata; значения совпали.
- Первый вход показывает понятный старт без фиктивной личной статистики. Пример недели открывается отдельно. Форма сразу показывает выбранные дни и объём недели. На телефоне есть отдельное меню разделов, возврат фокуса, targets ≥44px и поддержка тёмной темы Telegram.
- Добавлены native startup, приватный pg_dump helper и Compose с отдельной БД/прикладной ролью/миграцией. Docker build context исключает кластер и backup.

## Проверки

| Проверка | Результат / evidence |
|---|---|
| Go tests всей сборки | PASS, [go-tests.txt](evidence/postgresql/go-tests.txt) |
| SQL/file integration + race, включая pending/committed retry, auth claim, recovery, rollback/cancel/panic cleanup | PASS, [integration.txt](evidence/postgresql/integration.txt) |
| Предыдущий полный race-прогон этого этапа | PASS, [go-race.txt](evidence/postgresql/go-race.txt); последняя правка SQL cleanup дополнительно проверена targeted race выше |
| go vet / staticcheck | PASS, [vet](evidence/postgresql/go-vet.txt), [staticcheck](evidence/postgresql/staticcheck.txt) |
| JavaScript unit / syntax | 57/57, [unit](evidence/postgresql/frontend-tests.txt), [syntax](evidence/postgresql/frontend-check.txt) |
| Реальный браузер + PostgreSQL | 73/73: 20 базовых + 13 lifecycle + 10 питание/измерения + 10 AI contract fixtures + 12 accounts + 8 первый запуск/theme |
| RAT | 16/16, критические 5/5; [rat.json](evidence/postgresql/rat.json) |
| HTTP → PostgreSQL | 300/300 записей, 10 RPS, 30 секунд, request p95 5.766 мс; ошибок 0, drops 0; [нагрузка](evidence/postgresql/http-load.json), [тип хранилища](evidence/postgresql/storage-health.json) |
| Перенос, restart, SQL restore | PASS, [cutover.json](evidence/postgresql/cutover.json) |
| Compose | config validation PASS; контейнеры не запускались — Docker daemon недоступен. Native PostgreSQL проверен |

Нагрузка — локальный loopback smoke на 4 workers, вместе с обычной разработкой/проверками; это не предел оборудования, не 5-минутный production SLO и не доказательство 10 000 одновременно активных пользователей. Browser AI и Telegram проверки используют явно обозначенные fixtures, без внешних вызовов модели/бота.

## Оставшиеся границы

Публичные HTTPS/deploy, живой AI с серверным ключом и вход через настоящего Telegram-бота ещё не выполнены. Автоматические внешние backup/retention, восстановление размещённой инфраструктуры и multi-replica HTTP-нагрузка не заявлены. Auth writes пока сериализованы и читают ограниченный ledger; перед масштабированием требуется измерение входа/регистрации. Сохранение агрегата целиком и ограниченный receipt-журнал остаются явными контрактами. Экспертная проверка правил и пользовательский пилот по презентации техническими тестами не заменяются.

[Инструкция запуска и резервирования](postgresql.md) · [ADR](adr/008-postgresql-storage.md) · [первый экран](evidence/postgresql/first-run/welcome-desktop.png) · [мобильное меню](evidence/postgresql/first-run/mobile-menu.png).
