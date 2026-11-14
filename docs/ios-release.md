# Нативный iPhone-клиент Ритм

## Реализовано

`mobile/ios/Rhythm.xcodeproj`, общая scheme `Rhythm`, iPhone / iOS 17+, Swift 6.0 language mode. XcodeGen, CocoaPods, сторонние Swift packages и серверные ключи не нужны. В комплекте Info.plist, оригинальная непрозрачная иконка 1024×1024, asset catalog и privacy manifest.

Нативные SwiftUI экраны:

- **Сегодня:** текущий серверный день/часовой пояс, занятия, явная отметка выполнения/пропуска, независимый ввод сна и усталости.
- **План:** текущая и принятая будущая недели, отдельное предложение с причинами и явным подтверждением; отправляются revision и token именно показанного предложения.
- **Дневник:** сохранённые записи, показатели и подтверждаемое удаление.
- **Аккаунт:** вход логином/паролем, выход, JSON backup через системный Files exporter, отдельное удаление веб-аккаунта с паролем и подтверждением. Последнее использует `DELETE /api/account {password,revision,confirmDelete:true}`, а не очистку дневника.

Подтверждения принятия плана и удаления сохраняют показанный снимок. Обновление после возврата приложения на экран не подменяет его revision; новая запись с другого устройства приводит к конфликту вместо удаления непроверенных данных. Смена identity binding сбрасывает открытые подтверждения и пароль. Для удаления аккаунта это дополнительно проверяет Foundation-модель до HTTP-запроса.

Регистрация, профиль, восстановление, смена пароля, варианты занятий, HRV, состав тела, меню и AI доступны внутри явно названного расширенного режима WKWebView того же сайта. Перед открытием копируются только cookies origin. WebView использует непостоянное хранилище; при закрытии cookies синхронизируются обратно, старый binding сбрасывается, личность и состояние перечитываются. Навигация на другой origin и новые окна блокируются. Ссылки политики и поддержки открываются системным браузером: `/privacy.html`, `/support.html`.

Локальных уведомлений, push, HealthKit, оплаты, tracking SDK, медицинских заключений и офлайн-записи в этой версии нет. Экспорт представляет полный JSON сервера, включая поля, не отображаемые нативно. Экспортировать следует в безопасное место: файл содержит личный дневник. Offline означает ошибку соединения; успешное сохранение не имитируется.

## Границы и безопасность

`RhythmCore` — Foundation/Observation package: Codable модели, маленький потребительский `RhythmAPI`, HTTP adapter и MainActor model. UI имеет одного владельца model; запросы сериализуются через busy flag, фоновых циклов/Task.detached нет. URLSession использует cookie jar и `X-Rhythm-Session`; binding не заменяет cookie. При смене сессии state очищается. Любая неопределённая запись/409 блокирует следующие изменения до явного обновления; автоматического повтора нет. После обновления пользователь проверяет серверный результат и заново подтверждает изменение.

Новый аккаунт может иметь null profile/plan/decision/collections. Модели это поддерживают. Незнакомые новые поля ответа игнорируются Codable. Nullable отсутствующие показатели не превращаются в нули. Дневниковые ID генерируются UUID; существующие записи не изменяются оптимистично.

Base URL задаётся build setting `RHYTHM_BASE_URL`, исходно `https://176.119.147.97`. Запрещены HTTP, userinfo, path, query, fragment. URLSession не следует redirect, чтобы не передать чувствительные заголовки или тела другому адресу. Обычная системная проверка TLS обязательна. Приложение не хранит пароль и не пишет сетевые тела в логи; сессию хранит системный cookie jar. После подтверждённого удаления аккаунта cookie origin и binding очищаются.

Уведомления, транзакции клиента, retry queues, собственный DI framework: **N/A** — отсутствуют соответствующие операции. Атомарность удаления/дневника реализует сервер. Native UI не удаляет Telegram-аккаунт; веб-аккаунт и Telegram являются разными способами идентификации.

## Что проверено локально

Окружение содержит `/Library/Developer/CommandLineTools`, Swift 6.1.2 и macOS SDK. Xcode и iOS SDK отсутствуют. Проверены Foundation core build и Swift Testing suite с URLProtocol mock, структура plist/pbxproj и синтаксис SwiftUI-файлов. Это **не** сборка iOS target, не запуск на iPhone/simulator, не проверка подписи и не App Store validation.

```sh
CLANG_MODULE_CACHE_PATH=/tmp/rhythm-clang-cache \
SWIFTPM_MODULECACHE_OVERRIDE=/tmp/rhythm-swift-cache \
swift test --disable-sandbox --package-path mobile/ios/RhythmCore \
  --scratch-path /tmp/rhythm-swift-build --cache-path /tmp/rhythm-spm-cache

plutil -lint mobile/ios/Rhythm/Info.plist mobile/ios/Rhythm/PrivacyInfo.xcprivacy \
  mobile/ios/Rhythm.xcodeproj/project.pbxproj

swiftc -frontend -parse mobile/ios/Rhythm/RhythmApp.swift \
  mobile/ios/Rhythm/RootView.swift mobile/ios/Rhythm/WebScreen.swift
```

В обычном окружении с доступными SwiftPM caches достаточно `swift test --package-path mobile/ios/RhythmCore`. `--disable-sandbox` выше относится только к запуску SwiftPM compiler plugin в ограниченном рабочем окружении; sandbox приложения не отключается.

## Следующий обязательный этап на Xcode

1. Установить актуальный поддерживаемый Apple Xcode с iOS SDK. Не менять глобальный developer directory для проверки: можно передать `DEVELOPER_DIR=/Applications/Xcode.app/Contents/Developer` команде.
2. Открыть проект, выбрать собственный уникальный bundle ID вместо `com.example.rhythm`, свой Signing Team, нужную версию/build number. Simulator не требует платного membership; для TestFlight/App Store потребуется Apple Developer Program и App Store Connect.
3. Проверить, что HTTPS production сервер доступен с iPhone и certificate соответствует IP или будущему домену. При переходе на домен обновить `RHYTHM_BASE_URL` в обеих конфигурациях. Не добавлять ATS exceptions.
4. Собрать simulator target, провести сценарии ниже, затем Archive реального iOS target и Organizer Validate App. Название simulator нужно взять из установленного Xcode.

```sh
DEVELOPER_DIR=/Applications/Xcode.app/Contents/Developer \
xcodebuild -project mobile/ios/Rhythm.xcodeproj -scheme Rhythm \
  -destination 'generic/platform=iOS Simulator' -derivedDataPath /tmp/rhythm-derived \
  CODE_SIGNING_ALLOWED=NO build
```

Ручная проверка перед TestFlight:

- Чистый запуск → гость без профиля → расширенный режим → настройка профиля → возврат в native.
- Регистрация/резервный код в web → закрытие → native identity account; выход/вход; пароль не остаётся в полях.
- Самочувствие, done/missed, повторное исправление → запись действительно видна на сайте и после перезапуска.
- Две сессии: внешнее изменение → конфликт native → обновление → повторный просмотр/подтверждение. Смена аккаунта в WebView не переносит старую команду.
- Принятие предложения, пересчёт после отметки, календарный переход через серверный день и понедельник.
- Отсутствие сети, timeout, 401/409/503, плохой TLS: никаких ложных подтверждений, доступно восстановление после обновления.
- Экспорт через Files → JSON успешно восстанавливается штатным web import/restore в пустой тестовый аккаунт.
- Отдельный одноразовый тестовый аккаунт: неверный пароль удаления, stale revision, затем успешное удаление; прежние сеансы недействительны, вход невозможен. Проверять на PostgreSQL; file adapter может отвечать 503.
- VoiceOver, Dynamic Type, маленький экран, dark mode, клавиатура, доступность кнопок, поворот/возврат из background, cookie bridge и запрет внешней навигации.

## Перед публикацией

Исходники подготовлены для дальнейшей проверки. Дистрибутив IPA и подписанный архив не созданы; загрузка в App Store Connect не выполнялась. Отсутствие Apple Developer membership не мешает хранить/проверять исходники, но блокирует TestFlight/App Store distribution. Официальный процесс: [Apple distribution](https://developer.apple.com/documentation/xcode/distributing-your-app-for-beta-testing-and-releases/), [App Store Connect workflow](https://developer.apple.com/help/app-store-connect/get-started/app-store-connect-workflow).

Владелец должен указать юридически корректное имя разработчика, доступный контакт поддержки и действительную политику. Публичные `/privacy.html` и `/support.html` должны быть **развёрнуты и проверены**, наличие локальных файлов недостаточно. PrivacyInfo.xcprivacy описывает предусмотренные категории данных без tracking, но не заменяет заполнение App Privacy в App Store Connect: сверить с фактическим сервером, optional AI provider, сроками хранения/backup и данными в запросах. AI остаётся опциональным и требует существующего согласия в веб-интерфейсе.

Подготовить настоящие screenshots с simulator/device, возрастной рейтинг, описание без медицинских обещаний, review notes и отдельный review account. Не использовать личный аккаунт владельца. Перепроверить актуальные требования SDK перед submission: [Submitting](https://developer.apple.com/app-store/submitting/), [Review Guidelines](https://developer.apple.com/app-store/review/guidelines/). Native функциональность есть, но решение App Review и достаточность продукта не гарантируются.

Удаление аккаунта обязательно проверить end-to-end на развёрнутом сервере: очистка дневника сама по себе не заменяет удаление учётной записи. См. [Apple account deletion](https://developer.apple.com/support/offering-account-deletion-in-your-app/). За пределами основной БД отдельно документируются реальные сроки удаления резервных копий; приложение не обещает немедленное физическое удаление архивных backups.
