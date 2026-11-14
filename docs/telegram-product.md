# Telegram: вход в продукт и личные команды

Отдельный `rhythm-bot` открывает существующее приложение `/mini`. Он не читает дневник, не обращается к БД и не добавляет рассылки. `/start`, `/help`, `/privacy` отвечают только в личном чате пользователя с ботом; обычный текст, группы, сообщения ботов и команды другому боту игнорируются. Кнопка «Мой ритм» открывает HTTPS Mini App.

Вход сайта и Telegram остаются отдельными аккаунтами. AI требует согласия в приложении. Экспорт и очистка дневника доступны в настройках; это не обещание удаления аккаунта. Существующая серверная проверка Telegram initData остаётся границей доверия.

## Настройка

`TELEGRAM_BOT_TOKEN` и `PUBLIC_URL` передаются только через окружение. Не вставляйте токен в аргументы команд, URL терминала, скриншоты или логи. `PUBLIC_URL` — HTTPS origin без пути, query, fragment и credentials; приложение добавляет `/mini`.

```sh
python3 scripts/telegram_setup.py --check
python3 scripts/telegram_setup.py --apply
```

`--check` читает getMe, getWebhookInfo, меню и команды. `--apply` устанавливает команды, описание и default menu button, затем проверяет меню/команды. Скрипт не отправляет сообщения, не читает updates и не меняет webhook. Он выводит только username, публичный URL и флаги проверки. Неуспешная проверка возвращает ненулевой exit code. При частичном сбое повторный `--apply` безопасен: настройки заменяются теми же значениями.

Main Mini App в BotFather и его доступность для пользователей настраиваются отдельно; menu button не доказывает живой запуск iPhone Telegram.

## Runtime

```sh
go build -o rhythm-bot ./backend/cmd/bot
./rhythm-bot
```

Native unit: `deploy/native/rhythm-bot.service`, бинарник `/opt/rhythm/current/rhythm-bot`, окружение `/etc/rhythm/app.env` с тем же PUBLIC_URL и токеном, что у web. Файл окружения должен быть доступен только администратору. Бинарник должен входить в release bundle до включения unit.

Запускайте ровно один polling процесс на токен. Перед запуском runtime проверяет getMe/getWebhookInfo и отказывается работать при существующем webhook. Выбор другого consumer и удаление webhook — отдельное явное действие оператора. Telegram 409 (другой polling consumer), 401 и запрет доступа останавливают polling. Сам runtime не может гарантировать внешнюю блокировку чужих consumers; наблюдайте restart/error state unit и исправляйте владельца токена.

Один синхронный цикл владеет offset и запросами. SIGINT/SIGTERM отменяет запрос, Run возвращается, процесс дожидается завершения без оставленных goroutines. HTTP timeout 40 секунд ограничивает long polling в 30 секунд; ответы ограничены 1 MiB. Временные ошибки polling используют отменяемый backoff 1–30 секунд. Ошибки API не включают ответ Telegram, текст пользователя или credential-bearing URL; HTTP redirects запрещены в production client.

Offset монотонный в течение процесса: повторный update ID не вызывает повторный ответ. После попытки ответа offset продвигается даже при временной ошибке отправки: повторная отправка при неопределённом сетевом исходе могла бы дублировать сообщение. Пользователь может повторить команду. Offset подтверждается следующим getUpdates, поэтому crash между отправкой и подтверждением допускает повтор после рестарта — exactly-once доставка не заявляется. Постоянное хранение offset не вводится ради трёх безопасных команд.

## Проверка

```sh
go test -race ./backend/internal/telegrambot ./backend/cmd/bot
go vet ./backend/internal/telegrambot ./backend/cmd/bot
python3 -m unittest discover -s scripts -p test_telegram_setup.py
```

Fixtures проверяют фильтрацию приватных команд, offsets/duplicates, отмену long poll и redaction 401, отказ при webhook. Python tests проверяют origin, read-only check, apply и отсутствие изменений webhook/updates. Сетевые тесты обращаются только к httptest; реальные сообщения и платные API не вызываются.

Живой smoke оператора: `/start` в личном чате → кнопка → вход Mini App → сохранение и повторное открытие. Повторить в iPhone Telegram; проверить группы/обычный текст без ответа. Автотесты не заменяют этот smoke.
