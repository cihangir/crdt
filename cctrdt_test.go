package crdt

import "testing"

func initCCRDT(t *testing.T) *CRDT {

	ccrdt, err := New(
		[]string{
			// "192.168.59.103:6379",
			// "192.168.59.103:49153",
			"127.0.0.1:6379",
		})

	if err != nil {
		t.Fatal(err)
	}

	return ccrdt
}
