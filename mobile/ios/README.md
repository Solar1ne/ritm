# Ритм для iPhone

Откройте `Rhythm.xcodeproj` в Xcode с iOS SDK. Scheme: **Rhythm**, deployment target: **iOS 17**, Swift language mode: **6**. Нативные SwiftUI экраны работают с существующим Go API; локальный Swift Package `RhythmCore` не содержит сторонних зависимостей.

В Build Settings укажите собственные `PRODUCT_BUNDLE_IDENTIFIER` и Signing Team. `RHYTHM_BASE_URL` по умолчанию `https://176.119.147.97`; допускается только HTTPS origin с доверенным сертификатом. ATS exceptions и обхода TLS нет. Секреты сервера, пароли, ключи AI и Telegram в приложение не добавляются.

Руководство по проверке и публикации находится в [docs/ios-release.md](../../docs/ios-release.md).

```sh
swift test --package-path mobile/ios/RhythmCore
```

Команду запускайте из корня репозитория. При отсутствии Xcode тесты используют Swift Testing из Command Line Tools; не требуют XCTest или iOS simulator. Сами экраны требуют iOS SDK и не проверяются этой командой.
