package application

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestRequestHashIgnoresFieldOrderAndWhitespace(t *testing.T) {
	compact := `{"full_name":"Иван","birth_date":"1990-05-17","phone":"+79991234567","monthly_income":1,"employment_type":"employed","amount":2,"term_months":6}`
	reordered := `{
		"term_months": 6, "amount": 2, "employment_type": "employed",
		"monthly_income": 1, "phone": "+79991234567", "birth_date": "1990-05-17", "full_name": "Иван"
	}`
	var a, b Input
	if err := json.Unmarshal([]byte(compact), &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(reordered), &b); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(RequestHash(a), RequestHash(b)) {
		t.Fatal("hashes differ for the same request")
	}
	if len(RequestHash(a)) != 32 {
		t.Fatalf("hash length = %d, want 32", len(RequestHash(a)))
	}
}

func TestRequestHashDependsOnValues(t *testing.T) {
	in := validInput()
	other := in
	other.Amount++
	if bytes.Equal(RequestHash(in), RequestHash(other)) {
		t.Fatal("different requests have the same hash")
	}
}
