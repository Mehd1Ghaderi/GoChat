package scylla

import (
	"context"
	"errors"
	"time"

	"github.com/casbin/casbin/v2/model"
	"github.com/casbin/casbin/v2/persist"
	"github.com/gocql/gocql"
)

// CasbinAdapter stores casbin rules in the casbin_rules table.
type CasbinAdapter struct {
	repo    *Repository
	timeout time.Duration
}

var (
	_ persist.Adapter      = (*CasbinAdapter)(nil)
	_ persist.BatchAdapter = (*CasbinAdapter)(nil)
)

func (r *Repository) CasbinAdapter(timeout time.Duration) *CasbinAdapter {
	return &CasbinAdapter{repo: r, timeout: timeout}
}

type casbinRule struct {
	PType string `db:"ptype"`
	V0    string `db:"v0"`
	V1    string `db:"v1"`
	V2    string `db:"v2"`
	V3    string `db:"v3"`
	V4    string `db:"v4"`
	V5    string `db:"v5"`
}

func (c casbinRule) values() []string {
	vals := []string{c.V0, c.V1, c.V2, c.V3, c.V4, c.V5}
	n := len(vals)
	for n > 0 && vals[n-1] == "" {
		n--
	}
	return vals[:n]
}

func toRule(ptype string, rule []string) (casbinRule, error) {
	if len(rule) > 6 {
		return casbinRule{}, errors.New("casbin rule has more than 6 fields")
	}
	v := make([]string, 6)
	copy(v, rule)
	return casbinRule{PType: ptype, V0: v[0], V1: v[1], V2: v[2], V3: v[3], V4: v[4], V5: v[5]}, nil
}

func (a *CasbinAdapter) ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), a.timeout)
}

func (a *CasbinAdapter) LoadPolicy(m model.Model) error {
	ctx, cancel := a.ctx()
	defer cancel()
	for _, ptype := range []string{"p", "g"} {
		var rules []casbinRule
		if err := a.repo.query(ctx, "SELECT ptype, v0, v1, v2, v3, v4, v5 FROM casbin_rules WHERE ptype = ?", ptype).SelectRelease(&rules); err != nil {
			return wrap(err)
		}
		for _, r := range rules {
			if err := persist.LoadPolicyArray(append([]string{r.PType}, r.values()...), m); err != nil {
				return err
			}
		}
	}
	return nil
}

func (a *CasbinAdapter) SavePolicy(m model.Model) error {
	ctx, cancel := a.ctx()
	defer cancel()
	b := a.repo.batch(ctx, true)
	b.Query("DELETE FROM casbin_rules WHERE ptype = ?", "p")
	b.Query("DELETE FROM casbin_rules WHERE ptype = ?", "g")
	for _, sec := range []string{"p", "g"} {
		for ptype, ast := range m[sec] {
			for _, rule := range ast.Policy {
				r, err := toRule(ptype, rule)
				if err != nil {
					return err
				}
				insertRule(b.Query, r)
			}
		}
	}
	return a.repo.execBatch(b)
}

func insertRule(q func(string, ...any) *gocql.Batch, r casbinRule) {
	q("INSERT INTO casbin_rules (ptype, v0, v1, v2, v3, v4, v5) VALUES (?, ?, ?, ?, ?, ?, ?)", r.PType, r.V0, r.V1, r.V2, r.V3, r.V4, r.V5)
}

func (a *CasbinAdapter) AddPolicy(_ string, ptype string, rule []string) error {
	return a.AddPolicies("", ptype, [][]string{rule})
}

func (a *CasbinAdapter) AddPolicies(_ string, ptype string, rules [][]string) error {
	ctx, cancel := a.ctx()
	defer cancel()
	b := a.repo.batch(ctx, true)
	for _, rule := range rules {
		r, err := toRule(ptype, rule)
		if err != nil {
			return err
		}
		insertRule(b.Query, r)
	}
	return a.repo.execBatch(b)
}

func (a *CasbinAdapter) RemovePolicy(_ string, ptype string, rule []string) error {
	return a.RemovePolicies("", ptype, [][]string{rule})
}

func (a *CasbinAdapter) RemovePolicies(_ string, ptype string, rules [][]string) error {
	ctx, cancel := a.ctx()
	defer cancel()
	b := a.repo.batch(ctx, true)
	for _, rule := range rules {
		r, err := toRule(ptype, rule)
		if err != nil {
			return err
		}
		b.Query("DELETE FROM casbin_rules WHERE ptype = ? AND v0 = ? AND v1 = ? AND v2 = ? AND v3 = ? AND v4 = ? AND v5 = ?",
			r.PType, r.V0, r.V1, r.V2, r.V3, r.V4, r.V5)
	}
	return a.repo.execBatch(b)
}

func (a *CasbinAdapter) RemoveFilteredPolicy(_ string, ptype string, fieldIndex int, fieldValues ...string) error {
	ctx, cancel := a.ctx()
	defer cancel()
	var rules []casbinRule
	if err := a.repo.query(ctx, "SELECT ptype, v0, v1, v2, v3, v4, v5 FROM casbin_rules WHERE ptype = ?", ptype).SelectRelease(&rules); err != nil {
		return wrap(err)
	}
	var matched [][]string
	for _, r := range rules {
		vals := append(r.values(), make([]string, 6)...)[:6]
		ok := true
		for i, fv := range fieldValues {
			if fv != "" && (fieldIndex+i >= 6 || vals[fieldIndex+i] != fv) {
				ok = false
				break
			}
		}
		if ok {
			matched = append(matched, r.values())
		}
	}
	if len(matched) == 0 {
		return nil
	}
	return a.RemovePolicies("", ptype, matched)
}
