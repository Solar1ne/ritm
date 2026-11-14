package accounts

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

var (
	ErrUnauthorized = errors.New("account authentication required")
	ErrUnavailable  = errors.New("account storage unavailable")
	ErrLogin        = errors.New("invalid login")
	ErrPassword     = errors.New("invalid password")
	ErrNameTaken    = errors.New("login unavailable")
	ErrConflict     = errors.New("guest state changed")
	ErrCapacity     = errors.New("account storage capacity reached")
)

const (
	Lifetime       = 30 * 24 * time.Hour
	MaxAccounts    = 10000
	MaxSessions    = 50000
	MaxRevocations = 50000
)

type Account struct {
	Login          string       `json:"login"`
	Password       PasswordHash `json:"password"`
	RecoveryDigest string       `json:"recoveryDigest"`
	CreatedAt      time.Time    `json:"createdAt"`
}

type Session struct {
	Principal string    `json:"principal"`
	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// Ledger maps credentials and session digests to identities. Raw passwords,
// bearer tokens and recovery codes never enter this persistence contract.
type Ledger struct {
	Version       int                  `json:"version"`
	Accounts      map[string]Account   `json:"accounts"`
	Sessions      map[string]Session   `json:"sessions"`
	BlockedLegacy map[string]time.Time `json:"blockedLegacy"`
}

func EmptyLedger() Ledger {
	return Ledger{Version: 1, Accounts: map[string]Account{}, Sessions: map[string]Session{}, BlockedLegacy: map[string]time.Time{}}
}

func (l Ledger) Validate() error {
	if l.Version != 1 || l.Accounts == nil || l.Sessions == nil || l.BlockedLegacy == nil || len(l.Accounts) > MaxAccounts || len(l.Sessions) > MaxSessions || len(l.BlockedLegacy) > MaxRevocations {
		return ErrUnavailable
	}
	logins := make(map[string]bool, len(l.Accounts))
	for principal, a := range l.Accounts {
		login, err := NormalizeLogin(a.Login)
		if !ValidPrincipal(principal) || !strings.HasPrefix(principal, "u_") || err != nil || login != a.Login || logins[login] || !a.Password.valid() || len(a.RecoveryDigest) != 64 || !validDigest(a.RecoveryDigest) || a.CreatedAt.IsZero() {
			return ErrUnavailable
		}
		logins[login] = true
	}
	for digest, session := range l.Sessions {
		if !validDigest(digest) || !ValidPrincipal(session.Principal) || session.CreatedAt.IsZero() || !session.ExpiresAt.After(session.CreatedAt) {
			return ErrUnavailable
		}
		if strings.HasPrefix(session.Principal, "u_") {
			if _, ok := l.Accounts[session.Principal]; !ok {
				return ErrUnavailable
			}
		}
	}
	for principal, expiry := range l.BlockedLegacy {
		if !ValidPrincipal(principal) || expiry.IsZero() {
			return ErrUnavailable
		}
	}
	return nil
}

func validDigest(value string) bool { return ValidPrincipal("u_" + value) }

// Claim must be checked atomically with credential creation by the adapter.
// It lets the account use case attach an existing diary without importing its model.
type Claim struct {
	Guest    string
	Revision uint64
}

// Lookup bounds credential reads to one identity. An empty lookup requests a
// complete snapshot (health/import); repositories may return a validated superset.
type Lookup struct {
	Login, Principal, SessionDigest string
}

type Repository interface {
	ReadAccounts(context.Context, Lookup) (Ledger, error)
	UpdateAccounts(context.Context, *Claim, func(*Ledger) error) error
	CheckAccounts(context.Context) error
}

type Service struct{ repo Repository }

func New(repo Repository) (*Service, error) {
	if repo == nil {
		return nil, errors.New("account repository is required")
	}
	return &Service{repo: repo}, nil
}

func (s *Service) Check(ctx context.Context) error {
	return s.repo.CheckAccounts(ctx)
}

type Grant struct {
	Principal    string
	Token        string
	RecoveryCode string
	ExpiresAt    time.Time
}

func (s *Service) Register(ctx context.Context, guest string, revision uint64, username, password string, now time.Time) (Grant, error) {
	login, err := NormalizeLogin(username)
	if err != nil {
		return Grant{}, err
	}
	if err := ValidatePassword(password); err != nil {
		return Grant{}, err
	}
	if !ValidPrincipal(guest) || !strings.HasPrefix(guest, "g_") {
		return Grant{}, ErrConflict
	}
	if err := ctx.Err(); err != nil {
		return Grant{}, err
	}
	passwordHash, err := hashPassword(password)
	if err != nil {
		return Grant{}, fmt.Errorf("hash credential: %w", err)
	}
	code, digest, err := newRecovery()
	if err != nil {
		return Grant{}, err
	}
	grant, err := newGrant("u_"+guest[2:], now)
	if err != nil {
		return Grant{}, err
	}
	err = s.repo.UpdateAccounts(ctx, &Claim{Guest: guest, Revision: revision}, func(l *Ledger) error {
		l.clean(now)
		if _, exists := l.Accounts[grant.Principal]; exists {
			return ErrConflict
		}
		if _, _, exists := l.byLogin(login); exists {
			return ErrNameTaken
		}
		if len(l.Accounts) >= MaxAccounts {
			return ErrCapacity
		}
		l.Accounts[grant.Principal] = Account{Login: login, Password: passwordHash, RecoveryDigest: digest, CreatedAt: now}
		return l.addSession(grant, now)
	})
	if err != nil {
		return Grant{}, err
	}
	grant.RecoveryCode = code
	return grant, nil
}

func (s *Service) Login(ctx context.Context, username, password string, now time.Time) (Grant, error) {
	login, err := NormalizeLogin(username)
	if err != nil || len(password) > 512 {
		return Grant{}, ErrUnauthorized
	}
	l, err := s.repo.ReadAccounts(ctx, Lookup{Login: login})
	if err != nil {
		return Grant{}, err
	}
	principal, account, exists := l.byLogin(login)
	hash := account.Password
	if !exists {
		hash = dummyPassword()
	}
	valid := verifyPassword(hash, password)
	if !valid || !exists {
		return Grant{}, ErrUnauthorized
	}
	grant, err := newGrant(principal, now)
	if err != nil {
		return Grant{}, err
	}
	err = s.repo.UpdateAccounts(ctx, nil, func(current *Ledger) error {
		current.clean(now)
		latest, ok := current.Accounts[principal]
		if !ok || latest.Password != account.Password {
			return ErrUnauthorized
		}
		return current.addSession(grant, now)
	})
	if err != nil {
		return Grant{}, err
	}
	return grant, nil
}

func (s *Service) Resolve(ctx context.Context, bearer string, now time.Time) (string, error) {
	if len(bearer) != 43 {
		return "", ErrUnauthorized
	}
	l, err := s.repo.ReadAccounts(ctx, Lookup{SessionDigest: TokenHash(bearer)})
	if err != nil {
		return "", err
	}
	session, ok := l.Sessions[TokenHash(bearer)]
	if !ok || !session.ExpiresAt.After(now) {
		return "", ErrUnauthorized
	}
	return session.Principal, nil
}

func (s *Service) AllowLegacy(ctx context.Context, principal string, now time.Time) error {
	l, err := s.repo.ReadAccounts(ctx, Lookup{Principal: principal})
	if err != nil {
		return err
	}
	return l.AllowLegacy(principal, now)
}

func (l Ledger) AllowLegacy(principal string, now time.Time) error {
	if strings.HasPrefix(principal, "u_") {
		return ErrUnauthorized
	}
	if strings.HasPrefix(principal, "g_") {
		if _, ok := l.Accounts["u_"+principal[2:]]; ok {
			return ErrUnauthorized
		}
	}
	if expiry := l.BlockedLegacy[principal]; expiry.After(now) {
		return ErrUnauthorized
	}
	return nil
}

func (s *Service) NewTelegramSession(ctx context.Context, principal string, now time.Time) (Grant, error) {
	if !ValidPrincipal(principal) || !strings.HasPrefix(principal, "t_") {
		return Grant{}, ErrUnauthorized
	}
	grant, err := newGrant(principal, now)
	if err != nil {
		return Grant{}, err
	}
	err = s.repo.UpdateAccounts(ctx, nil, func(l *Ledger) error { l.clean(now); return l.addSession(grant, now) })
	if err != nil {
		return Grant{}, err
	}
	return grant, nil
}

func (s *Service) Logout(ctx context.Context, bearer, legacy string, now time.Time) error {
	return s.repo.UpdateAccounts(ctx, nil, func(l *Ledger) error {
		l.clean(now)
		if bearer != "" {
			delete(l.Sessions, TokenHash(bearer))
			return nil
		}
		if !ValidPrincipal(legacy) || strings.HasPrefix(legacy, "u_") {
			return nil
		}
		if len(l.BlockedLegacy) >= MaxRevocations {
			return ErrCapacity
		}
		l.BlockedLegacy[legacy] = now.Add(Lifetime)
		return nil
	})
}

type SessionInfo struct {
	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt"`
	Current   bool      `json:"current"`
}
type Info struct {
	Login     string        `json:"login"`
	CreatedAt time.Time     `json:"createdAt"`
	Sessions  []SessionInfo `json:"sessions"`
}

func (s *Service) Info(ctx context.Context, bearer string, now time.Time) (Info, error) {
	l, err := s.repo.ReadAccounts(ctx, Lookup{SessionDigest: TokenHash(bearer)})
	if err != nil {
		return Info{}, err
	}
	principal, account, err := l.accountSession(bearer, now)
	if err != nil {
		return Info{}, err
	}
	info := Info{Login: account.Login, CreatedAt: account.CreatedAt, Sessions: []SessionInfo{}}
	for digest, session := range l.Sessions {
		if session.Principal == principal && session.ExpiresAt.After(now) {
			info.Sessions = append(info.Sessions, SessionInfo{CreatedAt: session.CreatedAt, ExpiresAt: session.ExpiresAt, Current: digest == TokenHash(bearer)})
		}
	}
	sort.Slice(info.Sessions, func(i, j int) bool {
		if info.Sessions[i].Current != info.Sessions[j].Current {
			return info.Sessions[i].Current
		}
		return info.Sessions[i].CreatedAt.After(info.Sessions[j].CreatedAt)
	})
	return info, nil
}

func (s *Service) LogoutOthers(ctx context.Context, bearer string, now time.Time) error {
	return s.repo.UpdateAccounts(ctx, nil, func(l *Ledger) error {
		principal, _, err := l.accountSession(bearer, now)
		if err != nil {
			return err
		}
		for digest, session := range l.Sessions {
			if session.Principal == principal && digest != TokenHash(bearer) {
				delete(l.Sessions, digest)
			}
		}
		return nil
	})
}

// ChangePassword revokes every old session and rotates the recovery code in the
// same transaction; only the returned new session can remain authenticated.
func (s *Service) ChangePassword(ctx context.Context, bearer, oldPassword, newPassword string, now time.Time) (Grant, error) {
	if err := ValidatePassword(newPassword); err != nil {
		return Grant{}, err
	}
	l, err := s.repo.ReadAccounts(ctx, Lookup{SessionDigest: TokenHash(bearer)})
	if err != nil {
		return Grant{}, err
	}
	principal, account, err := l.accountSession(bearer, now)
	if err != nil || !verifyPassword(account.Password, oldPassword) {
		return Grant{}, ErrUnauthorized
	}
	passwordHash, err := hashPassword(newPassword)
	if err != nil {
		return Grant{}, err
	}
	code, digest, err := newRecovery()
	if err != nil {
		return Grant{}, err
	}
	grant, err := newGrant(principal, now)
	if err != nil {
		return Grant{}, err
	}
	err = s.repo.UpdateAccounts(ctx, nil, func(current *Ledger) error {
		id, latest, err := current.accountSession(bearer, now)
		if err != nil || id != principal || latest.Password != account.Password || latest.RecoveryDigest != account.RecoveryDigest {
			return ErrUnauthorized
		}
		latest.Password, latest.RecoveryDigest = passwordHash, digest
		current.Accounts[principal] = latest
		current.revokeAll(principal)
		current.clean(now)
		return current.addSession(grant, now)
	})
	if err != nil {
		return Grant{}, err
	}
	grant.RecoveryCode = code
	return grant, nil
}

func (s *Service) Recover(ctx context.Context, username, code, newPassword string, now time.Time) (string, error) {
	login, err := NormalizeLogin(username)
	if err != nil {
		return "", ErrUnauthorized
	}
	if err := ValidatePassword(newPassword); err != nil {
		return "", err
	}
	digest, err := recoveryDigest(code)
	if err != nil {
		return "", ErrUnauthorized
	}
	l, err := s.repo.ReadAccounts(ctx, Lookup{Login: login})
	if err != nil {
		return "", err
	}
	principal, account, found := l.byLogin(login)
	if !found || subtle.ConstantTimeCompare([]byte(digest), []byte(account.RecoveryDigest)) != 1 {
		return "", ErrUnauthorized
	}
	passwordHash, err := hashPassword(newPassword)
	if err != nil {
		return "", err
	}
	newCode, newDigest, err := newRecovery()
	if err != nil {
		return "", err
	}
	err = s.repo.UpdateAccounts(ctx, nil, func(current *Ledger) error {
		latest, ok := current.Accounts[principal]
		if !ok || latest.Password != account.Password || latest.RecoveryDigest != account.RecoveryDigest {
			return ErrUnauthorized
		}
		latest.Password, latest.RecoveryDigest = passwordHash, newDigest
		current.Accounts[principal] = latest
		current.revokeAll(principal)
		current.clean(now)
		return nil
	})
	if err != nil {
		return "", err
	}
	return newCode, nil
}

func (s *Service) RotateRecovery(ctx context.Context, bearer, password string, now time.Time) (string, error) {
	l, err := s.repo.ReadAccounts(ctx, Lookup{SessionDigest: TokenHash(bearer)})
	if err != nil {
		return "", err
	}
	principal, account, err := l.accountSession(bearer, now)
	if err != nil || !verifyPassword(account.Password, password) {
		return "", ErrUnauthorized
	}
	code, digest, err := newRecovery()
	if err != nil {
		return "", err
	}
	err = s.repo.UpdateAccounts(ctx, nil, func(current *Ledger) error {
		id, latest, err := current.accountSession(bearer, now)
		if err != nil || id != principal || latest.Password != account.Password || latest.RecoveryDigest != account.RecoveryDigest {
			return ErrUnauthorized
		}
		latest.RecoveryDigest = digest
		current.Accounts[principal] = latest
		return nil
	})
	if err != nil {
		return "", err
	}
	return code, nil
}

func (l Ledger) byLogin(login string) (string, Account, bool) {
	for principal, account := range l.Accounts {
		if account.Login == login {
			return principal, account, true
		}
	}
	return "", Account{}, false
}

func (l Ledger) accountSession(bearer string, now time.Time) (string, Account, error) {
	session, ok := l.Sessions[TokenHash(bearer)]
	if len(bearer) != 43 || !ok || !session.ExpiresAt.After(now) {
		return "", Account{}, ErrUnauthorized
	}
	account, ok := l.Accounts[session.Principal]
	if !ok {
		return "", Account{}, ErrUnauthorized
	}
	return session.Principal, account, nil
}

func newGrant(principal string, now time.Time) (Grant, error) {
	bearer, err := token()
	return Grant{Principal: principal, Token: bearer, ExpiresAt: now.Add(Lifetime)}, err
}

func (l *Ledger) clean(now time.Time) {
	for digest, session := range l.Sessions {
		if !session.ExpiresAt.After(now) {
			delete(l.Sessions, digest)
		}
	}
	for principal, expiry := range l.BlockedLegacy {
		if !expiry.After(now) {
			delete(l.BlockedLegacy, principal)
		}
	}
}

func (l *Ledger) revokeAll(principal string) {
	for digest, session := range l.Sessions {
		if session.Principal == principal {
			delete(l.Sessions, digest)
		}
	}
}

func (l *Ledger) addSession(grant Grant, now time.Time) error {
	// Keep at most ten active sessions per identity; displace the oldest deterministically.
	var own []string
	for digest, session := range l.Sessions {
		if session.Principal == grant.Principal {
			own = append(own, digest)
		}
	}
	sort.Slice(own, func(i, j int) bool {
		a, b := l.Sessions[own[i]], l.Sessions[own[j]]
		if a.CreatedAt.Equal(b.CreatedAt) {
			return own[i] < own[j]
		}
		return a.CreatedAt.Before(b.CreatedAt)
	})
	for len(own) >= 10 {
		delete(l.Sessions, own[0])
		own = own[1:]
	}
	if len(l.Sessions) >= MaxSessions {
		return ErrCapacity
	}
	l.Sessions[TokenHash(grant.Token)] = Session{Principal: grant.Principal, CreatedAt: now, ExpiresAt: grant.ExpiresAt}
	return nil
}
