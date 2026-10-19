package source

import "testing"

func TestDefaultAccountUseProxyBySource(t *testing.T) {
	if DefaultAccountUseProxy("baozimh") {
		t.Fatal("baozimh should default to direct connection")
	}
	if !DefaultAccountUseProxy("picacg") {
		t.Fatal("other sources should default to using the configured proxy")
	}
}

func TestApplyDefaultAccountUseProxyPreservesExplicitChoice(t *testing.T) {
	account := Account{}
	ApplyDefaultAccountUseProxy("baozimh", &account)
	if AccountUsesProxy(account) {
		t.Fatal("baozimh default should disable proxy")
	}
	SetAccountUseProxy(&account, true)
	ApplyDefaultAccountUseProxy("baozimh", &account)
	if !AccountUsesProxy(account) {
		t.Fatal("explicit proxy enablement should override the source default")
	}
}
