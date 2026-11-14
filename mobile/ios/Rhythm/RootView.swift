import SwiftUI
import RhythmCore
import UniformTypeIdentifiers

struct ExportDocument: FileDocument {
    static var readableContentTypes: [UTType] { [.json] }
    var data: Data
    init(data: Data) { self.data = data }
    init(configuration: ReadConfiguration) throws { data = configuration.file.regularFileContents ?? Data() }
    func fileWrapper(configuration: WriteConfiguration) throws -> FileWrapper { FileWrapper(regularFileWithContents: data) }
}

@MainActor struct RootView: View {
    @State private var model: AppModel
    let baseURL: URL
    @State private var web = false
    @State private var login = ""
    @State private var password = ""
    @State private var sleep = 7.0
    @State private var fatigue = 5
    @State private var includeSleep = true
    @State private var includeFatigue = true
    @State private var exportDocument = ExportDocument(data: Data())
    @State private var exporting = false
    @State private var acceptReview: AppState?
    @State private var deleteReview: (entry: Entry, state: AppState)?
    @State private var deletePassword = ""
    @State private var deleteAccountReview: AppState?
    @Environment(\.scenePhase) private var phase
    init(api: APIClient) {
        baseURL = api.baseURL
        _model = State(initialValue: AppModel(api: api))
    }
    var body: some View {
        VStack(spacing: 0) {
            if let error = model.errorMessage {
                VStack(alignment: .leading) {
                    Text(error).font(.callout).accessibilityLabel("Ошибка. " + error)
                    Button("Обновить данные") { Task { await model.refresh(resetIdentity: model.state == nil) } }
                }.padding().frame(maxWidth: .infinity, alignment: .leading).background(.orange.opacity(0.15))
            }
            if let state = model.state {
                TabView {
                    NavigationStack { today(state) }.tabItem { Label("Сегодня", systemImage: "sun.max") }
                    NavigationStack { plans(state) }.tabItem { Label("План", systemImage: "calendar") }
                    NavigationStack { diary(state) }.tabItem { Label("Дневник", systemImage: "book.closed") }
                    NavigationStack { account(state) }.tabItem { Label("Аккаунт", systemImage: "person.crop.circle") }
                }
            } else { NavigationStack { loginForm } }
            if model.busy { ProgressView("Связь с сервером…").padding(8) }
        }
        .tint(Color(red: 0.12, green: 0.43, blue: 0.36))
        .disabled(model.busy || web)
        .task { await model.refresh() }
        .onChange(of: phase) { _, phase in
            if phase == .active && !web { Task { await model.refresh() } }
        }
        .onChange(of: model.state?.sessionBinding) { _, _ in
            password = ""; deletePassword = ""; acceptReview = nil; deleteReview = nil; deleteAccountReview = nil
        }
        .sheet(isPresented: $web, onDismiss: { password = ""; deletePassword = ""; acceptReview = nil; deleteReview = nil; deleteAccountReview = nil; Task { await model.refresh(resetIdentity: true) } }) {
            WebScreen(baseURL: baseURL)
        }
        .fileExporter(isPresented: $exporting, document: exportDocument, contentType: .json, defaultFilename: "rhythm-backup") { result in
            if case .failure(let error) = result { model.errorMessage = error.localizedDescription }
        }
        .alert("Принять показанную неделю?", isPresented: Binding(get: { acceptReview != nil }, set: { if !$0 { acceptReview = nil } })) {
            Button("Принять") {
                guard let review = acceptReview, let plan = review.nextPlan,
                      model.state?.sessionBinding == review.sessionBinding else { return }
                Task { await model.save(.accept(revision: review.revision, weekStart: plan.weekStart, token: review.nextPlanToken)) }
                acceptReview = nil
            }
            Button("Отмена", role: .cancel) { acceptReview = nil }
        } message: { Text("Расписание сохранится только после подтверждения сервером.") }
        .alert("Удалить аккаунт навсегда?", isPresented: Binding(get: { deleteAccountReview != nil }, set: { if !$0 { deleteAccountReview = nil } })) {
            Button("Удалить аккаунт", role: .destructive) {
                guard let review = deleteAccountReview else { return }
                let secret = deletePassword; deletePassword = ""
                deleteAccountReview = nil
                Task { await model.deleteAccount(password: secret, reviewedState: review) }
            }
            Button("Отмена", role: .cancel) { deletePassword = ""; deleteAccountReview = nil }
        } message: { Text("Будут удалены аккаунт, дневник и связанные данные. Все сеансы будут отозваны. Это действие нельзя отменить.") }
        .alert("Удалить запись?", isPresented: Binding(get: { deleteReview != nil }, set: { if !$0 { deleteReview = nil } })) {
            Button("Удалить", role: .destructive) {
                if let review = deleteReview, model.state?.sessionBinding == review.state.sessionBinding {
                    Task { await model.save(.deleteEntry(revision: review.state.revision, id: review.entry.id)) }
                }
                deleteReview = nil
            }
            Button("Отмена", role: .cancel) { deleteReview = nil }
        }
    }
    private var loginForm: some View {
        Form {
            Section("Войти в Ритм") {
                TextField("Логин", text: $login).textContentType(.username).textInputAutocapitalization(.never).autocorrectionDisabled()
                SecureField("Пароль", text: $password).textContentType(.password)
                Button("Войти") {
                    let secret = password; password = ""
                    Task { await model.login(login, password: secret) }
                }.disabled(login.isEmpty || password.isEmpty)
            }
            Section {
                Button("Продолжить как гость") { Task { await model.refresh(resetIdentity: true) } }
                Button("Регистрация и восстановление") { web = true }
                Text("Пароль не сохраняется приложением. Гостевой доступ привязан к этому устройству.").font(.footnote)
            }
        }.navigationTitle("Ритм")
    }
    private func today(_ state: AppState) -> some View {
        Form {
            Section {
                Text(state.profile.map { "Привет, \($0.name)" } ?? "Ваш ритм начинается здесь").font(.title2.bold())
                Text("\(state.today) · \(state.timeZone)").font(.caption).foregroundStyle(.secondary)
                Text(state.decision?.title ?? "Настройте профиль, чтобы составить расписание.")
                if state.profile == nil { Button("Настроить профиль") { web = true } }
            }
            Section("Сегодня по плану") {
                let sessions = (state.plan?.sessions ?? []).filter { $0.date == state.today }
                if sessions.isEmpty { Text("На сегодня занятия не запланированы.") }
                ForEach(sessions) { session in
                    sessionRow(session)
                    HStack {
                        Button("Выполнено") { mark(session, status: "done", state: state) }
                        Spacer()
                        Button("Пропущено") { mark(session, status: "missed", state: state) }
                    }.buttonStyle(.bordered).disabled(model.needsRefresh)
                }
            }
            Section("Как вы сегодня?") {
                Toggle("Указать сон", isOn: $includeSleep)
                if includeSleep { Stepper("Сон: \(sleep, specifier: "%.1f") ч", value: $sleep, in: 0...24, step: 0.5) }
                Toggle("Указать усталость", isOn: $includeFatigue)
                if includeFatigue { Stepper("Усталость: \(fatigue) из 10", value: $fatigue, in: 1...10) }
                Button("Сохранить самочувствие") {
                    let entry = Entry(date: state.today, status: "wellbeing", sleep: includeSleep ? sleep : nil, fatigue: includeFatigue ? fatigue : nil)
                    Task { await model.save(.entry(revision: state.revision, entry)) }
                }.disabled(model.needsRefresh || (!includeSleep && !includeFatigue))
                Text("Повторная отметка исправит показатели за сегодня. Неуказанный показатель сохранится. Приложение не даёт медицинских рекомендаций.").font(.footnote)
            }
        }.navigationTitle("Сегодня").refreshable { await model.refresh() }
    }
    private func plans(_ state: AppState) -> some View {
        List {
            if let plan = state.plan { planSection("Текущая неделя", plan: plan) }
            if let accepted = state.acceptedNextPlan { planSection("Принято на следующую неделю", plan: accepted) }
            if let next = state.nextPlan {
                planSection("Предложение · ещё не принято", plan: next)
                Section {
                    ForEach(state.decision?.reasons ?? [], id: \.self) { Text($0).font(.callout) }
                    Button("Принять предложение") { acceptReview = state }.disabled(model.needsRefresh || state.acceptedNextPlan != nil)
                    if state.acceptedNextPlan != nil { Text("Для пересмотра сначала отмените принятую неделю в расширенном режиме.").font(.footnote) }
                }
            }
            Section { Button("Варианты занятий и история недель") { web = true } }
        }.navigationTitle("План").refreshable { await model.refresh() }
    }
    private func planSection(_ title: String, plan: Plan) -> some View {
        Section("\(title) · \(plan.weekStart)") {
            ForEach(plan.sessions ?? []) { sessionRow($0) }
            Text("Всего: \(plan.totalMinutes) минут").font(.caption)
        }
    }
    private func sessionRow(_ session: Session) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(session.title).font(.headline)
            Text("\(session.date) · \(session.detailsKnown ? "\(session.minutes) мин" : "длительность неизвестна")")
            Text(statusTitle(session.status)).font(.caption).foregroundStyle(.secondary)
        }.padding(.vertical, 4)
    }
    private func diary(_ state: AppState) -> some View {
        List {
            if (state.entries ?? []).isEmpty { Text("Записей пока нет. Отметьте самочувствие на вкладке «Сегодня».") }
            ForEach((state.entries ?? []).sorted { $0.date > $1.date }) { entry in
                VStack(alignment: .leading, spacing: 5) {
                    Text(entry.date).font(.headline)
                    Text(statusTitle(entry.status))
                    if let sleep = entry.sleep { Text("Сон: \(sleep, specifier: "%.1f") ч") }
                    if let fatigue = entry.fatigue { Text("Усталость: \(fatigue) из 10") }
                    Button("Удалить", role: .destructive) { deleteReview = (entry, state) }.disabled(model.needsRefresh)
                }
            }
            Section { Button("История, импорт и измерения") { web = true } }
        }.navigationTitle("Дневник").refreshable { await model.refresh() }
    }
    private func account(_ state: AppState) -> some View {
        Form {
            Section("Доступ") {
                Text(state.identity == "guest" ? "Гостевой дневник" : "Постоянный аккаунт")
                if state.identity == "guest" { loginFormSection }
                Button("Профиль, регистрация и безопасность") { web = true }
                Button("Выйти", role: .destructive) { Task { await model.logout() } }
            }
            Section("Ваши данные") {
                Button("Экспортировать резервную копию") {
                    Task { if let data = await model.export() { exportDocument = ExportDocument(data: data); exporting = true } }
                }.disabled(model.needsRefresh)
                Text("Копия содержит личный дневник. Выберите безопасное место хранения.").font(.footnote)
                Button("Питание, AI и управление данными") { web = true }
                Link("Поддержка", destination: baseURL.appendingPathComponent("support.html"))
                Link("Политика конфиденциальности", destination: baseURL.appendingPathComponent("privacy.html"))
            }
            if state.identity == "account" {
                Section("Удаление аккаунта") {
                    SecureField("Текущий пароль", text: $deletePassword).textContentType(.password)
                    Button("Удалить аккаунт навсегда", role: .destructive) { deleteAccountReview = state }
                        .disabled(deletePassword.isEmpty || model.needsRefresh)
                    Text("Отдельная операция: удаляет учётную запись и все связанные данные. Сначала можно сохранить экспорт.").font(.footnote)
                }
            }
        }.navigationTitle("Аккаунт")
    }
    private var loginFormSection: some View {
        Group {
            TextField("Логин", text: $login).textInputAutocapitalization(.never).autocorrectionDisabled().textContentType(.username)
            SecureField("Пароль", text: $password).textContentType(.password)
            Button("Войти в существующий аккаунт") { let secret = password; password = ""; Task { await model.login(login, password: secret) } }
                .disabled(login.isEmpty || password.isEmpty)
            Text("Гостевые записи не переносятся при входе. Для их сохранения зарегистрируйте аккаунт.").font(.footnote)
        }
    }
    private func mark(_ session: Session, status: String, state: AppState) {
        Task { await model.save(.entry(revision: state.revision, Entry(date: session.date, sessionId: session.id, status: status))) }
    }
    private func statusTitle(_ status: String) -> String {
        switch status { case "done": "Выполнено"; case "missed": "Пропущено"; case "wellbeing": "Самочувствие"; default: "Запланировано" }
    }
}
