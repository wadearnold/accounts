// Copyright 2019 The Moov Authors
// Use of this source code is governed by an Apache License
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"testing"
	"time"

	accounts "github.com/moov-io/accounts/client"
	"github.com/moov-io/base"
)

type testQLedgerAccountRepository struct {
	*qledgerAccountRepository

	deployment *qledgerDeployment
}

func (q *testQLedgerAccountRepository) close() {
	if q.deployment != nil {
		q.deployment.close()
	}
}

// qualifyQLedgerAccountTest will skip tests if Go's test -short flag is specified or if
// the needed env variables aren't set. See above for the env variables.
//
// Returned will be a qledgerAccountRepository
func qualifyQLedgerAccountTest(t *testing.T) *testQLedgerAccountRepository {
	t.Helper()

	if testing.Short() {
		t.Skip("-short flag enabled")
	}

	deployment := spawnQLedger(t)
	if deployment == nil {
		t.Fatal("nil QLedger docker deployment")
	}

	// repo, err := setupQLedgerAccountStorage("https://api.moov.io/v1/qledger", "moov") // Test against Production
	repo, err := setupQLedgerAccountStorage(fmt.Sprintf("http://localhost:%s", deployment.qledger.GetPort("7000/tcp")), "moov")
	if repo == nil || err != nil {
		t.Fatalf("repo=%v error=%v", repo, err)
	}
	if err := repo.Close(); err != nil { // should do nothing, so call in every test to make sure
		t.Fatal("QLedger .Close() is a no-op")
	}
	return &testQLedgerAccountRepository{repo, deployment}
}

func TestQLedgerAccounts__ping(t *testing.T) {
	repo := qualifyQLedgerAccountTest(t)
	defer repo.close()

	if err := repo.Ping(); err != nil {
		t.Error(err)
	}
}

func TestQLedger__Accounts(t *testing.T) {
	repo := qualifyQLedgerAccountTest(t)

	customerId, now := base.ID(), time.Now()
	future := now.Add(24 * time.Hour)
	account := &accounts.Account{
		Id:               base.ID(),
		CustomerId:       customerId,
		Name:             "example account",
		AccountNumber:    createAccountNumber(),
		RoutingNumber:    "121042882",
		Status:           "Active",
		Type:             "Checking",
		Balance:          100,
		BalancePending:   123,
		BalanceAvailable: 412,
		CreatedAt:        now,
		ClosedAt:         future,
		LastModified:     now,
	}
	if err := repo.CreateAccount(customerId, account); err != nil {
		t.Error(err)
	}

	// Now grab accounts for this customer
	accounts, err := repo.SearchAccountsByCustomerId(customerId)
	if err != nil {
		t.Error(err)
	}
	if len(accounts) == 0 {
		t.Fatal("no accounts found")
	}
	if account.Id != accounts[0].Id {
		t.Errorf("expected account %q, but found %#v", account.Id, accounts[0].Id)
	}
	if account.Balance != 100 || account.BalancePending != 123 || account.BalanceAvailable != 412 {
		t.Errorf("Balance=%d BalancePending=%d BalanceAvailable=%d", account.Balance, account.BalancePending, account.BalanceAvailable)
	}
	if account.CreatedAt.IsZero() {
		t.Error("zero time for CreatedAt")
	}

	// Grab accounts by their ID's
	account2 := *account
	account2.Id = base.ID()
	account2.Balance += 100
	account2.RoutingNumber = "231380104" // different value
	if err := repo.CreateAccount(customerId, &account2); err != nil {
		t.Fatal(err)
	}

	accounts, err = repo.GetAccounts([]string{account.Id, account2.Id})
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 2 {
		t.Errorf("got %d accounts", len(accounts))
	}
	if accounts[0].Id == account.Id {
		if accounts[1].Id != account2.Id {
			t.Errorf("mis-matching accounts")
		}
	} else {
		if accounts[1].Id != account.Id {
			t.Errorf("mis-matching accounts")
		}
	}

	// Search for account
	acct, err := repo.SearchAccountsByRoutingNumber(account.AccountNumber, account.RoutingNumber, "Checking")
	if err != nil {
		t.Fatal(err)
	}
	if acct == nil {
		t.Fatal("SearchAccounts: nil account")
	}
	if acct.Id != account.Id {
		t.Errorf("acct.Id=%q account.Id=%q", acct.Id, account.Id)
	}

	repo.close()
}

func TestQLedger__read(t *testing.T) {
	if v := readBalance("100"); v != 100 {
		t.Errorf("got %v", v)
	}
	if v := readBalance("asas"); v != 0 {
		t.Errorf("got %v", v)
	}

	if v := readTime("2019-01-02T15:04:05Z").Format(time.RFC3339); v != "2019-01-02T15:04:05Z" {
		t.Errorf("got %q", v)
	}
}
