package main

import (
	"github.com/Amitgb14/conch/internal/sandbox"
)

// Orca runs a recipe's create and resume and reads its result: a
// provisioned machine it connects to by ssh, with the checkout's root.
// It has no other way to the user name, so a token is given as it is
// here, and only here.

type orcaUserData struct {
	Provider string `json:"provider"`
	ID       string `json:"id"`
}

type orcaTarget struct {
	Label    string `json:"label"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
}

type orcaConnection struct {
	Type        string     `json:"type"`
	ProjectRoot string     `json:"projectRoot"`
	Target      orcaTarget `json:"target"`
}

type orcaResult struct {
	SchemaVersion int            `json:"schemaVersion"`
	CheckoutMode  string         `json:"checkoutMode"`
	Connection    orcaConnection `json:"connection"`
	UserData      orcaUserData   `json:"userData"`
}

// orcaFor is Orca's result for a sandbox reached as al. A key sandbox is
// given as its alias when ssh reads the aliases, so the key conch-sandbox
// handed over is the one offered (Orca's target names no key file); else,
// and for a token, as the address itself.
func orcaFor(r result, al alias, label string) orcaResult {
	host := al.Host
	if !al.Token && al.Identity != "" && includePresent() {
		host = al.name()
	}
	return orcaResult{
		SchemaVersion: 2,
		CheckoutMode:  "provisioned-root",
		Connection: orcaConnection{Type: "ssh", ProjectRoot: al.Root, Target: orcaTarget{
			Label: sandbox.ProviderLabel(r.Provider) + " " + label, Host: host, Port: al.Port, Username: al.User}},
		UserData: orcaUserData{Provider: r.Provider, ID: r.ID},
	}
}

func (a *app) sayOrca(r result, al alias, label string) error {
	return a.printJSON(orcaFor(r, al, label))
}
