package gosh

import (
	"sort"
	"strings"
)

func SplitKeyValue(kv string) (string, string) {
	parts := strings.SplitN(kv, "=", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	panic(kv)
}

func JoinKeyValue(k, v string) string {
	return k + "=" + v
}

func SortByKey(vars []string) {
	sort.Sort(keySorter(vars))
}

type keySorter []string

func (s keySorter) Len() int      { return len(s) }
func (s keySorter) Swap(i, j int) { s[i], s[j] = s[j], s[i] }
func (s keySorter) Less(i, j int) bool {
	ki, _ := SplitKeyValue(s[i])
	kj, _ := SplitKeyValue(s[j])
	return ki < kj
}

// SliceToMap converts slice of "k=v" entries to map, preferring later values
// over earlier ones.
func SliceToMap(s []string) map[string]string {
	m := make(map[string]string, len(s))
	for _, kv := range s {
		k, v := SplitKeyValue(kv)
		m[k] = v
	}
	return m
}

// MapToSlice converts map to slice of "k=v" entries.
func MapToSlice(m map[string]string) []string {
	s := make([]string, 0, len(m))
	for k, v := range m {
		s = append(s, JoinKeyValue(k, v))
	}
	SortByKey(s)
	return s
}

// MergeMaps merges the given maps into a new map, preferring values from later
// maps over those from earlier maps.
func MergeMaps(maps ...map[string]string) map[string]string {
	res := make(map[string]string)
	for _, m := range maps {
		for k, v := range m {
			res[k] = v
		}
	}
	return res
}
