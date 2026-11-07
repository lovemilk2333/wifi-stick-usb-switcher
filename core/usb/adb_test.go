package usb

import (
	"os"
	"os/user"
	"slices"
	"strconv"
	"testing"
)

func TestAdbdIdentityEnv(t *testing.T) {
	identity := &adbdIdentity{
		user: "alice",
		home: "/home/alice",
	}

	env := identity.env()
	for _, want := range []string{"HOME=/home/alice", "USER=alice", "LOGNAME=alice"} {
		if !slices.Contains(env, want) {
			t.Fatalf("identity env missing %q: %v", want, env)
		}
	}
}

func TestAppendGroupDeduplicates(t *testing.T) {
	groups := append_group(nil, 10)
	groups = append_group(groups, 20)
	groups = append_group(groups, 10)

	if !slices.Equal(groups, []uint32{10, 20}) {
		t.Fatalf("append_group() = %v, want [10 20]", groups)
	}
}

func TestResolveAdbdIdentityCurrentUser(t *testing.T) {
	account, err := user.Current()
	if err != nil {
		t.Skipf("cannot resolve current user: %v", err)
	}

	identity, err := resolve_adbd_identity(account.Username, "")
	if err != nil {
		t.Fatalf("resolve_adbd_identity() error: %v", err)
	}

	want_uid, err := strconv.ParseUint(account.Uid, 10, 32)
	if err != nil {
		t.Fatalf("invalid current uid %q: %v", account.Uid, err)
	}
	want_gid, err := strconv.ParseUint(account.Gid, 10, 32)
	if err != nil {
		t.Fatalf("invalid current gid %q: %v", account.Gid, err)
	}

	if identity.uid != uint32(want_uid) {
		t.Fatalf("uid = %d, want %d", identity.uid, want_uid)
	}
	if identity.gid != uint32(want_gid) {
		t.Fatalf("gid = %d, want %d", identity.gid, want_gid)
	}
	if identity.user != account.Username {
		t.Fatalf("user = %q, want %q", identity.user, account.Username)
	}
	if identity.home != account.HomeDir {
		t.Fatalf("home = %q, want %q", identity.home, account.HomeDir)
	}
	if !slices.Contains(identity.groups, identity.gid) {
		t.Fatalf("primary gid %d missing from groups %v", identity.gid, identity.groups)
	}
}

func TestAdbdHomeDirUsesAccountHome(t *testing.T) {
	root_account, err := user.Lookup("root")
	if err != nil {
		t.Skip("root account not available")
	}
	if got := adbdHomeDir("root"); got != root_account.HomeDir {
		t.Fatalf("adbdHomeDir(\"root\") = %q, want %q", got, root_account.HomeDir)
	}
	if os.Geteuid() == 0 {
		if got := adbdHomeDir(""); got != root_account.HomeDir {
			t.Fatalf("adbdHomeDir(\"\") = %q, want %q", got, root_account.HomeDir)
		}
	}
}

func TestResolveAdbdIdentityRootHome(t *testing.T) {
	account, err := user.Lookup("root")
	if err != nil {
		t.Skip("root account not available")
	}

	identity, err := resolve_adbd_identity("root", "")
	if err != nil {
		t.Fatalf("resolve_adbd_identity() error: %v", err)
	}
	if identity.home != account.HomeDir {
		t.Fatalf("root home = %q, want %q", identity.home, account.HomeDir)
	}
	if identity.user != "root" {
		t.Fatalf("user = %q, want root", identity.user)
	}
}

func TestResolveAdbdIdentityGroupOnly(t *testing.T) {
	account, err := user.Current()
	if err != nil {
		t.Skipf("cannot resolve current user: %v", err)
	}
	group, err := user.LookupGroupId(account.Gid)
	if err != nil {
		t.Skipf("cannot resolve current group: %v", err)
	}

	identity, err := resolve_adbd_identity("", group.Name)
	if err != nil {
		t.Fatalf("resolve_adbd_identity() error: %v", err)
	}

	want_gid, err := strconv.ParseUint(group.Gid, 10, 32)
	if err != nil {
		t.Fatalf("invalid group gid %q: %v", group.Gid, err)
	}
	if identity.uid != uint32(os.Geteuid()) {
		t.Fatalf("uid = %d, want daemon uid %d", identity.uid, os.Geteuid())
	}
	if identity.gid != uint32(want_gid) {
		t.Fatalf("gid = %d, want %d", identity.gid, want_gid)
	}
	if !slices.Contains(identity.groups, identity.gid) {
		t.Fatalf("primary gid %d missing from groups %v", identity.gid, identity.groups)
	}
}

func TestResolveAdbdIdentityExplicitGroup(t *testing.T) {
	account, err := user.Current()
	if err != nil {
		t.Skipf("cannot resolve current user: %v", err)
	}
	group, err := user.LookupGroupId(account.Gid)
	if err != nil {
		t.Skipf("cannot resolve current group: %v", err)
	}

	identity, err := resolve_adbd_identity(account.Username, group.Name)
	if err != nil {
		t.Fatalf("resolve_adbd_identity() error: %v", err)
	}

	want_gid, err := strconv.ParseUint(group.Gid, 10, 32)
	if err != nil {
		t.Fatalf("invalid group gid %q: %v", group.Gid, err)
	}
	if identity.gid != uint32(want_gid) {
		t.Fatalf("gid = %d, want %d", identity.gid, want_gid)
	}
	if !slices.Contains(identity.groups, identity.gid) {
		t.Fatalf("primary gid %d missing from groups %v", identity.gid, identity.groups)
	}
}
