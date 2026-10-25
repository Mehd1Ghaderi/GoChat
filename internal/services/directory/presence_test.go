package directory

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/anthdm/hollywood/actor"
	"github.com/gocql/gocql"

	"github.com/ssenerg/gochat/internal/actors"
	"github.com/ssenerg/gochat/internal/core/ids"
	"github.com/ssenerg/gochat/internal/services/hub"
)

func TestAcquaintancesShareATeam(t *testing.T) {
	repo := newTeamsRepo()
	me, bob, carol := repo.person("me"), repo.person("bob"), repo.person("carol")
	repo.join(ids.New(), time.Now(), me, bob)
	repo.join(ids.New(), time.Now(), carol)
	w, _ := newTeamsWorker(t, repo)
	got, err := w.acquaintances(context.Background(), Acquainted{
		UserID: me.String(), UserIDs: []string{bob.String(), carol.String(), "not-an-id", me.String()},
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(got)
	want := []string{me.String(), bob.String()}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestLeavingATeamEndsItsWatches(t *testing.T) {
	repo := newTeamsRepo()
	me, bob, carol := repo.person("me"), repo.person("bob"), repo.person("carol")
	acme, beta := ids.New(), ids.New()
	repo.join(acme, time.Now(), me, bob, carol)
	repo.join(beta, time.Now(), bob, carol)
	w, _ := newTeamsWorker(t, repo)
	// The hub knows me watches bob and carol, and they both watch me.
	var mu sync.Mutex
	var unwatched []hub.Unwatch
	w.Hub = w.Engine.SpawnFunc(func(c *actor.Context) {
		switch msg := c.Message().(type) {
		case hub.WatchedBy:
			actors.Reply(c, []string{bob.String(), carol.String()})
		case hub.WatchersOf:
			actors.Reply(c, []string{bob.String(), carol.String()})
		case hub.Unwatch:
			mu.Lock()
			unwatched = append(unwatched, msg)
			mu.Unlock()
		}
	}, "watches")
	// me leaves acme: they share no team with either any more.
	repo.leave(acme, me)
	w.recheckWatches(context.Background(), me)
	deadline := time.Now().Add(time.Second)
	for {
		mu.Lock()
		n := len(unwatched)
		mu.Unlock()
		if n == 3 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(unwatched) != 3 {
		t.Fatalf("unwatched %+v", unwatched)
	}
	mine := unwatched[0]
	slices.Sort(mine.Subjects)
	want := []string{bob.String(), carol.String()}
	slices.Sort(want)
	if mine.Watcher != me.String() || !slices.Equal(mine.Subjects, want) {
		t.Fatalf("my watches: %+v", mine)
	}
	for _, u := range unwatched[1:] {
		if !slices.Equal(u.Subjects, []string{me.String()}) || (u.Watcher != bob.String() && u.Watcher != carol.String()) {
			t.Fatalf("their watches: %+v", u)
		}
	}
}

// leave takes someone out of a team.
func (f *teamsRepo) leave(team, id gocql.UUID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.teams[team], id)
}
