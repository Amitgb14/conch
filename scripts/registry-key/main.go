// Command registry-key makes the key an integrations registry signs its
// index with, and signs one — for the registry's CI.
//
//	go run ./scripts/registry-key new
//	    prints the private key (for the registry's Actions secret
//	    CONCH_REGISTRY_SIGNING_KEY) and the public key (for
//	    integration.RegistryKey, or a registry's key in config.toml)
//	CONCH_REGISTRY_SIGNING_KEY=… go run ./scripts/registry-key sign index.json > index.json.sig
//	go run ./scripts/registry-key verify PUBLIC index.json index.json.sig
package main

import (
	"fmt"
	"os"

	"github.com/Amitgb14/conch/internal/integration"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "registry-key:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("new | sign FILE | verify PUBLIC FILE SIG")
	}
	switch {
	case args[0] == "new" && len(args) == 1:
		priv, pub, err := integration.NewKey()
		if err != nil {
			return err
		}
		fmt.Printf("private (keep secret, the registry's CONCH_REGISTRY_SIGNING_KEY): %s\npublic: %s\n", priv, pub)
		return nil
	case args[0] == "sign" && len(args) == 2:
		priv := os.Getenv("CONCH_REGISTRY_SIGNING_KEY")
		if priv == "" {
			return fmt.Errorf("set CONCH_REGISTRY_SIGNING_KEY")
		}
		data, err := os.ReadFile(args[1])
		if err != nil {
			return err
		}
		sig, err := integration.Sign(priv, data)
		if err != nil {
			return err
		}
		fmt.Println(sig)
		return nil
	case args[0] == "verify" && len(args) == 4:
		data, err := os.ReadFile(args[2])
		if err != nil {
			return err
		}
		sig, err := os.ReadFile(args[3])
		if err != nil {
			return err
		}
		if err := integration.Verify(args[1], data, sig); err != nil {
			return err
		}
		fmt.Println("ok")
		return nil
	}
	return fmt.Errorf("new | sign FILE | verify PUBLIC FILE SIG")
}
