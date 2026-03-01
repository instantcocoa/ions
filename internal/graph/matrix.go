package graph

import (
	"fmt"
	"sort"

	"github.com/emaland/ions/internal/workflow"
)

// MatrixCombination is a single combination of matrix values.
type MatrixCombination map[string]any

// ExpandMatrix takes a Matrix definition and produces all concrete combinations.
// 1. Compute the Cartesian product of all Dimensions
// 2. Remove any combination matching an Exclude entry
// 3. Apply Include entries: if an include matches an existing combo on its overlapping keys,
//    merge extra keys; otherwise add as new combo
func ExpandMatrix(m *workflow.Matrix) []MatrixCombination {
	if m == nil {
		return nil
	}

	// If the entire matrix is an expression, we cannot expand statically.
	if m.Expression != "" {
		return nil
	}

	// If there are no dimensions and no includes, return nil.
	if len(m.Dimensions) == 0 && len(m.Include) == 0 {
		return nil
	}

	// Build ordered dimension list (sorted by key for determinism).
	dimKeys := make([]string, 0, len(m.Dimensions))
	for k := range m.Dimensions {
		dimKeys = append(dimKeys, k)
	}
	sort.Strings(dimKeys)

	var combos []MatrixCombination

	if len(m.Dimensions) > 0 {
		// Build the struct slice for cartesianProduct.
		type dimPair struct {
			Key    string
			Values []any
		}
		dims := make([]dimPair, len(dimKeys))
		for i, k := range dimKeys {
			dims[i] = dimPair{Key: k, Values: m.Dimensions[k]}
		}

		// Convert to the expected type.
		cpDims := make([]struct {
			Key    string
			Values []any
		}, len(dims))
		for i, d := range dims {
			cpDims[i].Key = d.Key
			cpDims[i].Values = d.Values
		}

		combos = cartesianProduct(cpDims)
	}

	// Apply excludes: remove any combo that matches an exclude entry.
	if len(m.Exclude) > 0 {
		filtered := make([]MatrixCombination, 0, len(combos))
		for _, combo := range combos {
			excluded := false
			for _, excl := range m.Exclude {
				if comboMatchesEntry(combo, excl) {
					excluded = true
					break
				}
			}
			if !excluded {
				filtered = append(filtered, combo)
			}
		}
		combos = filtered
	}

	// Apply includes.
	for _, inc := range m.Include {
		merged := false
		// Determine which keys in this include overlap with dimension keys.
		overlapKeys := make([]string, 0)
		extraKeys := make([]string, 0)
		for k := range inc {
			if _, isDim := m.Dimensions[k]; isDim {
				overlapKeys = append(overlapKeys, k)
			} else {
				extraKeys = append(extraKeys, k)
			}
		}

		// Try to find an existing combo that matches on all overlap keys.
		// Only attempt merging if there are overlap keys; otherwise the include
		// has no dimension keys in common and should always be added as new.
		if len(overlapKeys) > 0 {
			for i, combo := range combos {
				if matchesOnKeys(combo, inc, overlapKeys) {
					// Merge extra keys into this combo.
					for _, ek := range extraKeys {
						combos[i][ek] = inc[ek]
					}
					merged = true
					// Don't break — GitHub merges into ALL matching combos.
				}
			}
		}

		if !merged {
			// Add as a new combo.
			newCombo := make(MatrixCombination, len(inc))
			for k, v := range inc {
				newCombo[k] = v
			}
			combos = append(combos, newCombo)
		}
	}

	if len(combos) == 0 {
		return nil
	}

	return combos
}

// cartesianProduct computes the Cartesian product of dimension value lists.
func cartesianProduct(dims []struct {
	Key    string
	Values []any
}) []MatrixCombination {
	if len(dims) == 0 {
		return nil
	}

	// Start with a single empty combination.
	result := []MatrixCombination{{}}

	for _, dim := range dims {
		var expanded []MatrixCombination
		for _, combo := range result {
			for _, val := range dim.Values {
				newCombo := make(MatrixCombination, len(combo)+1)
				for k, v := range combo {
					newCombo[k] = v
				}
				newCombo[dim.Key] = val
				expanded = append(expanded, newCombo)
			}
		}
		result = expanded
	}

	return result
}

// comboMatchesEntry returns true if for every key in entry, the combo has the same value.
func comboMatchesEntry(combo MatrixCombination, entry map[string]any) bool {
	for k, ev := range entry {
		cv, ok := combo[k]
		if !ok {
			return false
		}
		if fmt.Sprintf("%v", cv) != fmt.Sprintf("%v", ev) {
			return false
		}
	}
	return true
}

// matchesOnKeys returns true if combo and entry have the same values for all specified keys.
func matchesOnKeys(combo MatrixCombination, entry map[string]any, keys []string) bool {
	for _, k := range keys {
		cv, cok := combo[k]
		ev, eok := entry[k]
		if !cok || !eok {
			return false
		}
		if fmt.Sprintf("%v", cv) != fmt.Sprintf("%v", ev) {
			return false
		}
	}
	return true
}
