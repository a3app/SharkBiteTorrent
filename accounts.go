package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"sharkbite/internal/secret"
	"sharkbite/internal/vpnprov"
)

// VPN provider accounts, stored encrypted in accounts.json (one per provider).

func (a *App) accountsPath() string { return filepath.Join(a.configDir, "accounts.json") }

func (a *App) loadAccounts() map[string]*vpnprov.Account {
	accs := map[string]*vpnprov.Account{}
	if data, err := secret.ReadFile(a.accountsPath()); err == nil {
		json.Unmarshal(data, &accs)
	}
	return accs
}

func (a *App) saveAccounts(accs map[string]*vpnprov.Account) error {
	data, _ := json.Marshal(accs)
	return secret.WriteFile(a.accountsPath(), data)
}

func (a *App) account(provider string) *vpnprov.Account {
	return a.loadAccounts()[provider]
}

// --- Bound to the frontend ---

func (a *App) ListVpnProviders() []vpnprov.Info { return vpnprov.All() }

func (a *App) GetVpnRegions(provider string) ([]vpnprov.Region, error) {
	p, ok := vpnprov.Get(provider)
	if !ok {
		return nil, fmt.Errorf("unknown provider %q", provider)
	}
	// With no VPN connected this request would go out on the real
	// connection. Only do that for a provider the user signed in to (they're
	// about to connect to it, which shows it their IP anyway), not for every
	// provider they click on while browsing Settings.
	a.mu.Lock()
	noTunnel, mode := a.tun == nil, a.settings.VpnProvider
	a.mu.Unlock()
	if noTunnel && mode != "none" && a.account(provider) == nil {
		return []vpnprov.Region{}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return p.Regions(ctx)
}

type LoginResult struct {
	Display string `json:"display"` // set on success
	// NeedField is set when the provider needs one more input (2FA code,
	// confirmation); show it and call LoginVpnAccount again.
	NeedField *vpnprov.Field `json:"needField"`
	Message   string         `json:"message"`
}

func (a *App) LoginVpnAccount(provider string, creds map[string]string) (LoginResult, error) {
	p, ok := vpnprov.Get(provider)
	if !ok {
		return LoginResult{}, fmt.Errorf("unknown provider %q", provider)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	acc, err := p.Login(ctx, creds)
	var need *vpnprov.NeedFieldError
	if errors.As(err, &need) {
		return LoginResult{NeedField: &need.Field, Message: need.Message}, nil
	}
	if err != nil {
		return LoginResult{}, err
	}
	accs := a.loadAccounts()
	if old := accs[provider]; old != nil {
		p.Logout(ctx, old) // free the previous device/session slot
	}
	accs[provider] = acc
	if err := a.saveAccounts(accs); err != nil {
		return LoginResult{}, err
	}
	return LoginResult{Display: acc.Display}, nil
}

// LogoutVpnAccount signs out and forgets the stored credentials. If that
// account was in use, the app falls back to WARP.
func (a *App) LogoutVpnAccount(provider string) error {
	accs := a.loadAccounts()
	acc := accs[provider]
	if acc == nil {
		return nil
	}
	if p, ok := vpnprov.Get(provider); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		p.Logout(ctx, acc)
		cancel()
	}
	delete(accs, provider)
	var err error
	if len(accs) == 0 {
		err = os.Remove(a.accountsPath())
	} else {
		err = a.saveAccounts(accs)
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	a.mu.Lock()
	inUse := a.settings.VpnProvider == "account" && a.settings.AccountProvider == provider
	s := a.settings
	a.mu.Unlock()
	if inUse {
		s.VpnProvider = "warp"
		return a.SaveSettings(s)
	}
	return nil
}
