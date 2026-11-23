// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

package model

import (
	"errors"
	"fmt"
	"strings"
)

// Selector limits keep a hostile query string cheap to parse and match.
const (
	MaxSelectorBytes        = 1024
	MaxSelectorRequirements = 16
	MaxSelectorValues       = 16
	// MaxLabels caps spec.labels on create.
	MaxLabels = 64
)

type selectorOp int

const (
	opEquals selectorOp = iota
	opNotEquals
	opIn
	opNotIn
	opExists
	opNotExists
)

type requirement struct {
	key    string
	op     selectorOp
	values []string
}

// Selector is a parsed label selector. All requirements must match (AND).
// An empty Selector matches everything. Grammar (a subset of the Kubernetes
// label selector): `k=v`, `k==v`, `k!=v`, `k in (a,b)`, `k notin (a,b)`,
// `k` (key exists) and `!k` (key absent), separated by commas.
type Selector []requirement

// ParseSelector parses s. Keys and values must satisfy the same patterns as
// machine labels.
func ParseSelector(s string) (Selector, error) {
	if len(s) > MaxSelectorBytes {
		return nil, fmt.Errorf("selector is longer than %d bytes", MaxSelectorBytes)
	}
	if trimSp(s) == "" {
		return nil, nil
	}
	parts, err := splitTopLevel(s)
	if err != nil {
		return nil, err
	}
	if len(parts) > MaxSelectorRequirements {
		return nil, fmt.Errorf("selector has more than %d requirements", MaxSelectorRequirements)
	}
	sel := make(Selector, 0, len(parts))
	for _, p := range parts {
		r, err := parseRequirement(trimSp(p))
		if err != nil {
			return nil, err
		}
		sel = append(sel, r)
	}
	return sel, nil
}

// splitTopLevel splits on commas that are not inside parentheses.
func splitTopLevel(s string) ([]string, error) {
	var parts []string
	depth, start := 0, 0
	for i, c := range s {
		switch c {
		case '(':
			depth++
			if depth > 1 {
				return nil, errors.New("selector parentheses cannot nest")
			}
		case ')':
			depth--
			if depth < 0 {
				return nil, errors.New("selector has an unbalanced ')'")
			}
		case ',':
			if depth == 0 {
				parts = append(parts, s[start:i])
				start = i + 1
			}
		}
	}
	if depth != 0 {
		return nil, errors.New("selector has an unbalanced '('")
	}
	return append(parts, s[start:]), nil
}

func checkKey(k string) error {
	if !labelKey.MatchString(k) {
		return fmt.Errorf("invalid selector key %q", k)
	}
	return nil
}

func checkValue(v string) error {
	if !labelValue.MatchString(v) || trimSp(v) != v {
		return fmt.Errorf("invalid selector value %q", v)
	}
	return nil
}

func parseRequirement(p string) (requirement, error) {
	if p == "" {
		return requirement{}, errors.New("selector has an empty requirement")
	}
	if i := strings.IndexByte(p, '('); i >= 0 {
		head := strings.Fields(p[:i])
		if len(head) != 2 || (head[1] != "in" && head[1] != "notin") || !strings.HasSuffix(p, ")") {
			return requirement{}, fmt.Errorf("invalid selector requirement %q", p)
		}
		if err := checkKey(head[0]); err != nil {
			return requirement{}, err
		}
		raw := strings.Split(p[i+1:len(p)-1], ",")
		if len(raw) > MaxSelectorValues {
			return requirement{}, fmt.Errorf("selector set has more than %d values", MaxSelectorValues)
		}
		vals := make([]string, 0, len(raw))
		for _, v := range raw {
			v = trimSp(v)
			if v == "" {
				return requirement{}, errors.New("selector set contains an empty value")
			}
			if err := checkValue(v); err != nil {
				return requirement{}, err
			}
			vals = append(vals, v)
		}
		op := opIn
		if head[1] == "notin" {
			op = opNotIn
		}
		return requirement{key: head[0], op: op, values: vals}, nil
	}
	if strings.ContainsAny(p, "()") {
		return requirement{}, fmt.Errorf("invalid selector requirement %q", p)
	}
	if i := strings.Index(p, "!="); i >= 0 {
		return eqRequirement(p[:i], p[i+2:], opNotEquals)
	}
	if i := strings.Index(p, "=="); i >= 0 {
		return eqRequirement(p[:i], p[i+2:], opEquals)
	}
	if i := strings.IndexByte(p, '='); i >= 0 {
		return eqRequirement(p[:i], p[i+1:], opEquals)
	}
	if strings.HasPrefix(p, "!") {
		k := trimSp(p[1:])
		if err := checkKey(k); err != nil {
			return requirement{}, err
		}
		return requirement{key: k, op: opNotExists}, nil
	}
	if err := checkKey(p); err != nil {
		return requirement{}, err
	}
	return requirement{key: p, op: opExists}, nil
}

func eqRequirement(k, v string, op selectorOp) (requirement, error) {
	k, v = trimSp(k), trimSp(v)
	if err := checkKey(k); err != nil {
		return requirement{}, err
	}
	if err := checkValue(v); err != nil {
		return requirement{}, err
	}
	return requirement{key: k, op: op, values: []string{v}}, nil
}

// Matches reports whether labels satisfy every requirement. As in
// Kubernetes, `!=` and `notin` also match a machine that lacks the key.
func (s Selector) Matches(labels map[string]string) bool {
	for _, r := range s {
		v, has := labels[r.key]
		switch r.op {
		case opEquals, opIn:
			if !has || !contains(r.values, v) {
				return false
			}
		case opNotEquals, opNotIn:
			if has && contains(r.values, v) {
				return false
			}
		case opExists:
			if !has {
				return false
			}
		case opNotExists:
			if has {
				return false
			}
		}
	}
	return true
}

func contains(vals []string, v string) bool {
	for _, x := range vals {
		if x == v {
			return true
		}
	}
	return false
}

// trimSp trims only ASCII spaces so control characters stay visible to validation.
func trimSp(s string) string { return strings.Trim(s, " ") }
