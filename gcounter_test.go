package ccrdt

import (
	"strconv"
	"testing"
)

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

	count, err := gCounter.Sum()
	if err != nil {
		t.Fatal(err)
	}

	if count != 7 {
		t.Fatal("counts are not same")
	}

}
