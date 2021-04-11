package bfd

import "sort"

func GreaterUInt32(n ...uint32) uint32 {
	max := uint32(0)
	for _, u := range n {
		if u > max {
			max = u
		}
	}
	return max
}

func UniqueStringsSorted(str []string) []string {
	uniq := make([]string, len(str))
	copy(uniq, str)
	if len(uniq) < 2 {
		return uniq
	}
	sort.Strings(uniq)
	src := 1
	dst := 1
	prev := uniq[0]
	for src < len(uniq) {
		if uniq[src] == prev {
			src++
			continue
		}
		uniq[dst] = uniq[src]
		prev = uniq[src]
		src++
		dst++
	}
	return uniq[:dst]
}
