package authz_test

import (
	"testing"

	"github.com/ssenerg/gochat/internal/authz"
	"github.com/ssenerg/gochat/internal/authz/authztest"
)

func TestAccountAdministrationIsForSystemAdmins(t *testing.T) {
	az := authztest.New(t)
	cases := []struct {
		obj authz.Object
		act authz.Action
	}{
		{authz.ObjUser, authz.ActFind},
		{authz.ObjUser, authz.ActDeactivate},
		{authz.ObjSession, authz.ActManageAny},
		{authz.ObjStorage, authz.ActManageAny},
	}
	for _, c := range cases {
		if !az.Can(c.obj, c.act, "system_admin") {
			t.Errorf("a system admin may not %s %s", c.act, c.obj)
		}
		for _, role := range []string{"system_user", "team_admin", "channel_admin"} {
			if az.Can(c.obj, c.act, role) {
				t.Errorf("%s may %s %s", role, c.act, c.obj)
			}
		}
	}
	if !az.Can(authz.ObjFile, authz.ActUpload, "channel_member") {
		t.Error("the migrations' policy did not load")
	}
}
