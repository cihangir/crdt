package ccrdt

import "testing"

func initCCRDT(t *testing.T) *CCRDT {

	ccrdt, err := New(
		[]string{
			"192.168.59.103:6379",
			"192.168.59.103:49153",
		})

	if err != nil {
		t.Fatal(err)
	}

	return ccrdt
}
