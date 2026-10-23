package password

import "testing"

var params = Params{Time: 1, Memory: 8 * 1024, Threads: 1}

func TestHashVerify(t *testing.T) {
	h := Hash("correct horse", params)
	ok, rehash, err := Verify("correct horse", h, params)
	if err != nil || !ok || rehash {
		t.Fatalf("verify: ok=%v rehash=%v err=%v", ok, rehash, err)
	}
	if ok, _, _ := Verify("wrong horse", h, params); ok {
		t.Fatal("wrong password accepted")
	}
	stronger := Params{Time: 2, Memory: 8 * 1024, Threads: 1}
	if _, rehash, _ := Verify("correct horse", h, stronger); !rehash {
		t.Fatal("changed parameters should ask for a rehash")
	}
}

func TestVerifyRejectsMalformed(t *testing.T) {
	for _, h := range []string{"", "plain", "$argon2i$v=19$m=8192,t=1,p=1$c2FsdA$a2V5", "$argon2id$v=19$m=99999999,t=1,p=1$c2FsdA$a2V5"} {
		if _, _, err := Verify("x", h, params); err == nil {
			t.Errorf("accepted %q", h)
		}
	}
}
