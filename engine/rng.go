package engine

import "math/rand/v2"

// streamFor derives an independent, order-independent RNG for a single
// (seed, pathIndex) pair. Deriving the state from the path index instead of
// drawing from a shared, mutex-protected source is what makes outputs
// bit-identical regardless of worker scheduling: path i always consumes the
// same stream of normals.
//
// splitMix64 (the same mixer used to seed PCG) decorrelates nearby indices.
func streamFor(seed uint64, pathIndex int) *rand.Rand {
	return rand.New(rand.NewPCG(splitMix64(seed^uint64(pathIndex)*0x9E3779B97F4A7C15),
		splitMix64(seed^uint64(pathIndex)*0xD1B54A32D192ED03)))
}

// jobSeed derives a per-job seed from the batch seed and the job ordinal.
// When a stable tag is available it is mixed in instead, making the stream
// a function of (baseSeed, tag): reordering jobs or changing worker counts
// never moves streams between jobs. Untagged jobs fall back to the ordinal.
func jobSeed(baseSeed uint64, ordinal int) uint64 {
	return splitMix64(baseSeed ^ (uint64(ordinal)+1)*0x9E3779B97F4A7C15)
}

func taggedSeed(baseSeed uint64, tag string) uint64 {
	var h uint64 = 0xCBF29CE484222325
	for i := 0; i < len(tag); i++ {
		h ^= uint64(tag[i])
		h *= 0x100000001B3
	}
	return splitMix64(baseSeed ^ h ^ 0xA5A5A5A5A5A5A5A5)
}

func splitMix64(x uint64) uint64 {
	x += 0x9E3779B97F4A7C15
	x = (x ^ (x >> 30)) * 0xBF58476D1CE4E5B9
	x = (x ^ (x >> 27)) * 0x94D049BB133111EB
	return x ^ (x >> 31)
}
