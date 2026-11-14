import SwiftUI
import WebKit

/// Advanced flows share the same first-party cookie, never a token in a URL or JavaScript.
@MainActor struct WebScreen: View {
    let baseURL: URL
    @Environment(\.dismiss) private var dismiss
    @State private var webView: WKWebView?
    @State private var closing = false
    @State private var error: String?
    var body: some View {
        NavigationStack {
            VStack {
                if let error { Text(error).padding().foregroundStyle(.red) }
                if let webView { WebContent(webView: webView, baseURL: baseURL, error: $error) }
                else { ProgressView("Открываем расширенный режим…") }
            }
            .navigationTitle("Ритм · расширенный режим")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) {
                    Button("Готово") { Task { await close() } }.disabled(closing || webView == nil)
                }
                ToolbarItem(placement: .navigationBarLeading) {
                    Button("Обновить") { webView?.reload() }.disabled(closing)
                }
            }
        }
        .interactiveDismissDisabled()
        .task {
            guard webView == nil else { return }
            let config = WKWebViewConfiguration()
            config.websiteDataStore = .nonPersistent()
            for cookie in HTTPCookieStorage.shared.cookies(for: baseURL) ?? [] {
                await config.websiteDataStore.httpCookieStore.setCookie(cookie)
            }
            let view = WKWebView(frame: .zero, configuration: config)
            view.isInspectable = false
            webView = view
        }
    }
    private func close() async {
        closing = true
        guard let view = webView else { closing = false; return }
        view.stopLoading()
        let cookies = await view.configuration.websiteDataStore.httpCookieStore.allCookies()
        for cookie in HTTPCookieStorage.shared.cookies(for: baseURL) ?? [] { HTTPCookieStorage.shared.deleteCookie(cookie) }
        // Only the approved origin can navigate in this web view.
        for cookie in cookies where cookie.domain.trimmingCharacters(in: CharacterSet(charactersIn: ".")) == baseURL.host {
            HTTPCookieStorage.shared.setCookie(cookie)
        }
        dismiss()
    }
}
@MainActor private struct WebContent: UIViewRepresentable {
    let webView: WKWebView
    let baseURL: URL
    @Binding var error: String?
    func makeCoordinator() -> Coordinator { Coordinator(baseURL: baseURL, error: $error) }
    func makeUIView(context: Context) -> WKWebView {
        webView.navigationDelegate = context.coordinator
        webView.load(URLRequest(url: baseURL))
        return webView
    }
    func updateUIView(_ uiView: WKWebView, context: Context) {}
    @MainActor final class Coordinator: NSObject, WKNavigationDelegate {
        let baseURL: URL
        var error: Binding<String?>
        init(baseURL: URL, error: Binding<String?>) { self.baseURL = baseURL; self.error = error }
        func webView(_ webView: WKWebView, decidePolicyFor action: WKNavigationAction, decisionHandler: @escaping (WKNavigationActionPolicy) -> Void) {
            guard let url = action.request.url,
                  url.scheme == "https", url.host == baseURL.host, url.port == baseURL.port,
                  action.targetFrame != nil else {
                error.wrappedValue = "Эта ссылка открывает другой сервис. Используйте его отдельно в браузере."
                decisionHandler(.cancel); return
            }
            decisionHandler(.allow)
        }
        func webView(_ webView: WKWebView, didFailProvisionalNavigation navigation: WKNavigation!, withError error: Error) {
            self.error.wrappedValue = "Страница недоступна: " + error.localizedDescription
        }
        func webView(_ webView: WKWebView, didFail navigation: WKNavigation!, withError error: Error) {
            self.error.wrappedValue = "Страница недоступна: " + error.localizedDescription
        }
        func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) { error.wrappedValue = nil }
    }
}
