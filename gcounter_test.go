// Package crdt provides Convergent and Commutative Replicated Data Types
package crdt

import (
	"strconv"
	"testing"
)

// https://github.com/kevinwallace/crdt/blob/master/crdt.go
// https://github.com/aphyr/meangirls/blob/master/README.markdown
func initGCounter(t *testing.T) *GCounter {

	ccrdt := initCCRDT(t)

	key := "test" + strconv.FormatInt(ccrdt.sessions.randomSource.Int63(), 10)
	return ccrdt.NewGCounter(key)
}

func TestGCounterInitialization(t *testing.T) {
	initGCounter(t)
}

func TestGCounterIncrement(t *testing.T) {
	gCounter := initGCounter(t)
	err := gCounter.Add(1)
	if err != nil {
		t.Fatal(err)
	}

	err = gCounter.Add(5)
	if err != nil {
		t.Fatal(err)
	}

	err = gCounter.Add(1)
	if err != nil {
		t.Fatal(err)
	}

	count, err := gCounter.Merge()
	if err != nil {
		t.Fatal(err)
	}

	if count != 7 {
		t.Fatal("counts are not same")
	}

}
