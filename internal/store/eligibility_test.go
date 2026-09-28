package store

import (
	"encoding/json"
	"testing"
)

// Being in an organization is not being answerable for it.
func TestOnlyOwnerAndAdminCanDeclareAnAccount(t *testing.T) {
	for _, c := range []struct {
		role string
		want bool
	}{
		{RoleOwner, true},
		{RoleAdmin, true},
		{"member", false},
		{"billing_manager", false},
		{"", false},
	} {
		if got := (Account{Owner: "acme", Role: c.role}).CanDeclare(); got != c.want {
			t.Errorf("role %q: CanDeclare = %v, want %v", c.role, got, c.want)
		}
	}
}

// The interface must not re-derive eligibility from the role string: two derivations of one fact
// are two answers waiting to disagree, and this one decides what may be claimed as owned.
func TestAnAccountCarriesItsEligibilityToTheInterface(t *testing.T) {
	out, err := json.Marshal(Account{Owner: "acme", Kind: AccountOrganization, Role: "member"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal %s: %v", out, err)
	}
	if _, ok := got["canDeclare"]; !ok {
		t.Fatalf("%s carries no canDeclare, so the interface has to guess", out)
	}
	if got["canDeclare"] != false {
		t.Errorf("canDeclare = %v for a member, want false", got["canDeclare"])
	}
	// The role travels too: "you cannot declare this" is more useful beside the reason why.
	if got["role"] != "member" {
		t.Errorf("role = %v, want it carried alongside", got["role"])
	}
}
