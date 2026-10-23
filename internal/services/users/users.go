// Package users is a read-through cache of user records, shared by every
// actor that shows who did what.
package users

import (
	"context"
	"time"

	"github.com/gocql/gocql"

	"github.com/ssenerg/gochat/internal/core/cache"
	"github.com/ssenerg/gochat/internal/core/models"
)

type Repo interface {
	GetUser(ctx context.Context, id gocql.UUID) (*models.User, error)
	GetUsers(ctx context.Context, ids []gocql.UUID) ([]*models.User, error)
}

type Cache struct {
	repo  Repo
	cache *cache.Cache[gocql.UUID, *models.User]
}

func NewCache(repo Repo, capacity int, ttl time.Duration) *Cache {
	return &Cache{repo: repo, cache: cache.New[gocql.UUID, *models.User](capacity, ttl)}
}

func (c *Cache) Get(ctx context.Context, id gocql.UUID) (*models.User, error) {
	if u, ok := c.cache.Get(id); ok {
		return u, nil
	}
	u, err := c.repo.GetUser(ctx, id)
	if err != nil {
		return nil, err
	}
	c.cache.Set(id, u)
	return u, nil
}

// GetMany returns the users found among ids.
func (c *Cache) GetMany(ctx context.Context, idList []gocql.UUID) (map[gocql.UUID]*models.User, error) {
	out := make(map[gocql.UUID]*models.User, len(idList))
	var missing []gocql.UUID
	for _, id := range idList {
		if _, done := out[id]; done {
			continue
		}
		if u, ok := c.cache.Get(id); ok {
			out[id] = u
		} else {
			out[id] = nil
			missing = append(missing, id)
		}
	}
	for start := 0; start < len(missing); start += 100 {
		end := min(start+100, len(missing))
		found, err := c.repo.GetUsers(ctx, missing[start:end])
		if err != nil {
			return nil, err
		}
		for _, u := range found {
			c.cache.Set(u.ID, u)
			out[u.ID] = u
		}
	}
	for id, u := range out {
		if u == nil {
			delete(out, id)
		}
	}
	return out, nil
}

func (c *Cache) Invalidate(id gocql.UUID) {
	c.cache.Delete(id)
}
