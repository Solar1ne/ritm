# ADR-08: PostgreSQL для дневников и аккаунтов

Дата: 17.10.2026. Статус: реализовано; локальный сайт переключён. Заменяет решение о текущем хранилище из ADR-03. HTTP API и доменные правила сохраняются.

Раньше дневники и ledger аккаунтов хранились в атомарно заменяемых JSON-файлах. Этот вариант требовал единственного процесса. Теперь `internal/postgresstore` реализует существующие интерфейсы потребителей; `cmd/server` явно собирает зависимости. Добавлен pgx/v5: необходимый драйвер/пул для PostgreSQL; ORM и DI framework не вводятся.

Дневник остаётся одним ограниченным агрегатом JSONB, потому что профиль, снимки недель, отметки, receipts, измерения и меню изменяются атомарно. `revision bigint` и проверка содержимого JSONB не допускают расхождения версии. Схема нормализует identity, account, session digest и legacy revocation. Уникальные login/owner, FK, ограничения формата и индексы защищают инварианты на уровне SQL. SQL-аналитика отдельных фактов не заявляется: для неё потребуется отдельное решение, а не случайное множество GIN-индексов.

Обычная запись блокирует только строку дневника (`SELECT FOR UPDATE`). После ожидания проверяются владелец и revision. Одновременное создание пустого дневника использует отдельные INSERT ON CONFLICT и SELECT. Регистрация захватывает auth advisory transaction lock, затем ту же строку дневника; account, sessions, revoke и смена owner фиксируются вместе. Старый guest больше не может читать/писать присоединённый дневник. Дополнительный generated FK запрещает удаление дневника существующего аккаунта.

Проверка авторизации читает индексированный срез ledger в Repeatable Read; health не сканирует хеши всех пользователей. Изменение аккаунтов сохраняет прежний callback-контракт: ограниченный полный snapshot под advisory lock, SQL пишет только diff. Это компромисс совместимости, а не обещание массового параллельного входа: 10 000 accounts/50 000 sessions — защитные пределы, не доказанная производительность.

Пул принадлежит composition root: до 10 соединений, connect 3s, statement 5s, lock 3s, idle transaction 10s. Контекст проходит во все операции. Rollback после отмены ограничен отдельными 2s; пул закрывается после завершения HTTP-запросов. Неопределённый результат COMMIT не ретраится автоматически; клиент перечитывает состояние и сохраняет idempotency ID.

Удалённые primary и fallback подключения требуют TLS с проверкой сертификата/hostname; `prefer`, `require` и plaintext downgrade отклоняются до Dial. Локальный loopback/unix socket и изолированный compose-host `db` разрешены без TLS. PostgreSQL-ошибки наружу содержат только SQLSTATE, без DSN, запроса и строк данных. Приложение использует роль без SUPERUSER/CREATEDB/CREATEROLE.

Схема применяется отдельной командой `rhythm-db migrate`, в транзакции с advisory lock и checksum. Startup не меняет схему. Неизвестная версия или изменённый checksum останавливают запуск. Текущий runner поддерживает baseline 001; следующая миграция должна расширить runner, сохраняя уже применённый SQL неизменным.

Перенос выполняется офлайн: остановка прежнего бинарника → приватная копия всего DATA_DIR → проба копии → preflight → fsync pending marker → SQL transaction с read-back всех дневников/ledger/ключа → completed marker. Даже pending запрещает файловый fallback. Повтор того же fingerprint не перезаписывает более новые SQL-записи. Другой fingerprint или непустая чужая цель отклоняются. Импорт ограничен 128 MiB/10 000 diaries, чтобы не скрывать неограниченную память/длительность; перенос большего объёма требует отдельного инструмента.

Ключ сессий переносится без замены: он также определяет Telegram principal. Непустая БД без ключа или несовпадающий SESSION_SECRET останавливают запуск. Утерянный ключ восстанавливают из бэкапа; произвольная ротация требует отдельной миграции идентичностей. Даты account/session хранятся с точностью PostgreSQL timestamptz (микросекунды); версии и числа в JSONB проверяются без float-преобразования.

N/A: брокеры, фоновые workers, распределённые транзакции и автоматические retry не используются. RLS не добавлена: SQL доступен только доверенному backend, пользователь не получает прямого подключения. Изоляция проверяется на границе каждого repository-метода и в SQL-транзакции.

Проверено: два пула/конкурирующие записи, оба порядка claim vs write, fault rollback, cancellation/pool cleanup, однократное recovery, checksum/DDL rollback, перенос/retry после pending и committed, сохранение ключа, JSONB size/precision, SQL backup/restore. [Evidence](../evidence/postgresql/integration.txt), [переключение](../evidence/postgresql/cutover.json), [инструкция запуска](../postgresql.md).

Основания: [PostgreSQL row/advisory locks](https://www.postgresql.org/docs/16/explicit-locking.html), [JSONB](https://www.postgresql.org/docs/16/datatype-json.html), [pgxpool BeginTx: rollback lifecycle](https://pkg.go.dev/github.com/jackc/pgx/v5/pgxpool#Pool.BeginTx).
