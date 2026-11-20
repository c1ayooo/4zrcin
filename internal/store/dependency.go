package store

// This file owns the one provenance inference the ledger makes: a fact cannot
// look more certain than the premises of the direction that produced it.
//
// The granularity is the direction, not the individual claim. A declaration's
// own from_fact_ids are not recorded (model.Fact has no such field, and the
// declaration contract no longer offers one), so the only edges that exist are:
//
//	up(f)   = the hard premises (intent.FromFactIDs) of the direction that produced f
//	down(f) = facts produced by directions that name f as a hard premise, transitively
//
// The price is stated rather than hidden: one lost premise demotes every fact of
// the same direction, including facts that did not rest on it. The alternative —
// inventing a per-claim edge the writer never recorded — would be a fabricated
// dependency, which is worse than a coarse true one.
//
// Nothing here reads or writes a new field: S2 changes the value written into
// Confidence, S3 changes Status, and the reason rides in the existing version
// journal.

import (
	"slices"

	"github.com/kksty/tianwu/internal/model"
)

// premiseLostReason prefixes the version-journal reason written on the rows a
// lost premise used to back.
const premiseLostReason = "premise_lost:"

// premiseChangedReason prefixes the journal reason written when a premise's
// assertion changed under rows that were verified against the old one. A verdict
// on v1 says nothing about v2, so the backing is gone even though the row is
// still there.
const premiseChangedReason = "premise_changed:"

// upstreamUnverifiedLocked returns the first premise of the direction that
// produced f whose status is not verified.
//
// A direction with no premises (a first touch) has none: the row speaks for
// itself. A premise id that names no row counts as unverified — nothing in the
// ledger vouches for it, and readiness already refuses to dispatch on it.
func (s *Store) upstreamUnverifiedLocked(f model.Fact) (string, bool) {
	if f.IntentID == "" {
		return "", false
	}
	for i := range s.db.Intents {
		in := &s.db.Intents[i]
		if in.ID != f.IntentID {
			continue
		}
		for _, id := range in.FromFactIDs {
			if id == "" || id == f.ID {
				continue
			}
			p, ok := s.factByIDLocked(id)
			if !ok || p.Status != "verified" {
				return id, true
			}
		}
		return "", false
	}
	return "", false
}

// factByIDLocked returns the live row for id. Caller holds the lock.
func (s *Store) factByIDLocked(id string) (*model.Fact, bool) {
	for i := range s.db.Facts {
		if s.db.Facts[i].ID == id {
			return &s.db.Facts[i], true
		}
	}
	return nil, false
}

// factsDownstreamLocked returns the indexes of the facts that rest on id,
// transitively. The direction graph is already cycle-checked
// (validateIntentStructureLocked), and id is seeded as seen, so a direction
// naming its own output as a premise cannot re-enter.
func (s *Store) factsDownstreamLocked(id string) []int {
	if id == "" {
		return nil
	}
	seenFact := map[string]bool{id: true}
	seenIntent := map[string]bool{}
	queue := []string{id}
	var out []int
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for i := range s.db.Intents {
			in := &s.db.Intents[i]
			if seenIntent[in.ID] || !slices.Contains(in.FromFactIDs, cur) {
				continue
			}
			seenIntent[in.ID] = true
			for j := range s.db.Facts {
				f := &s.db.Facts[j]
				if f.IntentID != in.ID || seenFact[f.ID] {
					continue
				}
				seenFact[f.ID] = true
				out = append(out, j)
				queue = append(queue, f.ID)
			}
		}
	}
	return out
}

// propagatePremiseLossLocked withdraws the endorsement id gave: every verified
// fact below it returns to unverified, and their ids are returned so the
// version journal can say why.
//
// It deliberately does not reject them — a lost premise removes backing, it does
// not prove the conclusion false — and it leaves rows that were never verified
// alone, so their own verdict stands.
func (s *Store) propagatePremiseLossLocked(id string) []string {
	var changed []string
	for _, j := range s.factsDownstreamLocked(id) {
		if s.db.Facts[j].Status != "verified" {
			continue
		}
		s.db.Facts[j].Status = "unverified"
		changed = append(changed, s.db.Facts[j].ID)
	}
	return changed
}

// flushFactChangesLocked commits pending in-memory writes and labels the rows in
// ids with reason in the version journal. The journal is the only place a
// reader can see *why* a verified row went back to unverified: Status alone
// cannot tell "a premise was withdrawn" from "nobody ever ran a review".
//
// ids empty writes no reason at all, so an ordinary status change is never
// labelled as a propagation.
func (s *Store) flushFactChangesLocked(reason string, ids []string, kinds ...string) error {
	if len(ids) > 0 && reason != "" {
		s.changeReason = reason
		s.changeReasonIDs = make(map[string]bool, len(ids))
		for _, id := range ids {
			s.changeReasonIDs[id] = true
		}
	}
	err := s.flush(kinds...)
	s.changeReason, s.changeReasonIDs = "", nil
	return err
}
