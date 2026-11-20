package main

import "testing"

func TestAccountForChunkRoundRobin(t *testing.T) {
	accounts := []modalAccount{{name: "1"}, {name: "2"}, {name: "3"}, {name: "4"}}
	want := []string{"1", "2", "3", "4", "1", "2", "3", "4", "1", "2"}
	for i, w := range want {
		if got := accountForChunk(accounts, i).name; got != w {
			t.Errorf("chunk %d: got account %s, want %s", i, got, w)
		}
	}
}

func TestAccountForChunkFewerAccounts(t *testing.T) {
	accounts := []modalAccount{{name: "1"}, {name: "3"}}
	want := []string{"1", "3", "1", "3", "1"}
	for i, w := range want {
		if got := accountForChunk(accounts, i).name; got != w {
			t.Errorf("chunk %d: got account %s, want %s", i, got, w)
		}
	}
}

func setModalEnv(t *testing.T, n string, url, key, secret string) {
	t.Helper()
	t.Setenv("MODAL_ENQUEUE_URL_"+n, url)
	t.Setenv("MODAL_PROXY_AUTH_TOKEN_ID_"+n, key)
	t.Setenv("MODAL_PROXY_AUTH_TOKEN_SECRET_"+n, secret)
}

func TestLoadModalAccounts(t *testing.T) {
	// Start from a clean slate for all four sets.
	for _, n := range []string{"1", "2", "3", "4"} {
		setModalEnv(t, n, "", "", "")
	}

	t.Run("none configured", func(t *testing.T) {
		if _, err := loadModalAccounts(); err == nil {
			t.Fatal("expected error when no account is configured")
		}
	})

	t.Run("skips unset accounts and keeps order", func(t *testing.T) {
		setModalEnv(t, "1", "http://a", "k1", "s1")
		setModalEnv(t, "3", "http://c", "k3", "s3")
		got, err := loadModalAccounts()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 2 || got[0].name != "1" || got[1].name != "3" {
			t.Fatalf("got %+v, want accounts 1 and 3", got)
		}
		setModalEnv(t, "1", "", "", "")
		setModalEnv(t, "3", "", "", "")
	})

	t.Run("partial account is an error", func(t *testing.T) {
		setModalEnv(t, "2", "http://b", "k2", "")
		if _, err := loadModalAccounts(); err == nil {
			t.Fatal("expected error for partially configured account")
		}
	})
}
