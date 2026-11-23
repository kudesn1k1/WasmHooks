package cluster

import (
	"crypto/sha256"
	"encoding/binary"
	"slices"
	"sort"
	"strconv"
)

const virtualNodes = 128

type point struct {
	hash uint64
	id   string
}

type ring struct {
	points []point
	ids    []string
}

func hash(value string) uint64 {
	sum := sha256.Sum256([]byte(value))
	return binary.BigEndian.Uint64(sum[:8])
}

func newRing(ids []string) ring {
	r := ring{ids: slices.Clone(ids), points: make([]point, 0, len(ids)*virtualNodes)}
	slices.Sort(r.ids)
	for _, id := range r.ids {
		for i := range virtualNodes {
			r.points = append(r.points, point{hash: hash(id + "\x00" + strconv.Itoa(i)), id: id})
		}
	}
	sort.Slice(r.points, func(i, j int) bool {
		if r.points[i].hash == r.points[j].hash {
			return r.points[i].id < r.points[j].id
		}
		return r.points[i].hash < r.points[j].hash
	})
	return r
}

func (r ring) owners(tenant, hook string) []string {
	key := hash(tenant + "\x00" + hook)
	start := sort.Search(len(r.points), func(i int) bool { return r.points[i].hash >= key })
	owners := make([]string, 0, len(r.ids))
	for offset := range len(r.points) {
		id := r.points[(start+offset)%len(r.points)].id
		if !slices.Contains(owners, id) {
			owners = append(owners, id)
		}
		if len(owners) == len(r.ids) {
			break
		}
	}
	return owners
}
