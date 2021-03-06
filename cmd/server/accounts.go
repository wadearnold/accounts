// Copyright 2019 The Moov Authors
// Use of this source code is governed by an Apache License
// license that can be found in the LICENSE file.

package main

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"strings"
	"time"

	accounts "github.com/moov-io/accounts/client"
	"github.com/moov-io/base"
	moovhttp "github.com/moov-io/base/http"

	"github.com/go-kit/kit/log"
	"github.com/gorilla/mux"
)

var (
	defaultRoutingNumber = os.Getenv("DEFAULT_ROUTING_NUMBER")

	errNoCustomerId = errors.New("no Customer ID found")
)

func getCustomerId(w http.ResponseWriter, r *http.Request) string {
	v, ok := mux.Vars(r)["customerId"]
	if !ok || v == "" {
		moovhttp.Problem(w, errNoCustomerId)
		return ""
	}
	return v
}

func addAccountRoutes(logger log.Logger, r *mux.Router, accountRepo accountRepository, transactionRepo transactionRepository) {
	r.Methods("POST").Path("/accounts").HandlerFunc(createAccount(logger, accountRepo, transactionRepo))
	r.Methods("GET").Path("/accounts/search").HandlerFunc(searchAccounts(logger, accountRepo))
}

// searchAccounts will attempt to find Accounts which match all query parameters. Searching with an account number will only
// return one account. Otherwise a 404 will be returned. '400 Bad Request' will be returned if query parameters are missing.
func searchAccounts(logger log.Logger, repo accountRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w, err := wrapResponseWriter(logger, w, r)
		if err != nil {
			return
		}

		q := r.URL.Query()

		// Search for a single account
		reqAcctNumber, reqRoutingNumber, reqAcctType := q.Get("number"), q.Get("routingNumber"), q.Get("type")
		if reqAcctNumber != "" && reqRoutingNumber != "" && reqAcctType != "" {
			// Grab and return accounts
			account, err := repo.SearchAccountsByRoutingNumber(reqAcctNumber, reqRoutingNumber, reqAcctType)
			if err != nil || account == nil {
				if requestId := moovhttp.GetRequestId(r); requestId != "" {
					logger.Log("accounts", fmt.Sprintf("%v", err), "requestId", requestId)
				}
				moovhttp.Problem(w, fmt.Errorf("account not found, err=%v", err))
				return
			}

			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode([]*accounts.Account{account})
			return
		}

		// Search based on CustomerId
		if customerId := q.Get("customerId"); customerId != "" {
			accounts, err := repo.SearchAccountsByCustomerId(customerId)
			if err != nil || len(accounts) == 0 {
				if requestId := moovhttp.GetRequestId(r); requestId != "" {
					logger.Log("accounts", fmt.Sprintf("%v", err), "requestId", requestId)
				}
				moovhttp.Problem(w, fmt.Errorf("account not found, err=%v", err))
				return
			}

			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(accounts)
			return
		}

		// Error if we didn't quit early from query params
		moovhttp.Problem(w, errors.New("missing account search query parameters"))
	}
}

type createAccountRequest struct {
	CustomerId string `json:"customerId"`
	Balance    int    `json:"balance"`
	Name       string `json:"name"`
	Type       string `json:"type"`
}

func (r createAccountRequest) validate() error {
	if r.CustomerId = strings.TrimSpace(r.CustomerId); r.CustomerId == "" {
		return errors.New("createAccountRequest: empty customerId")
	}
	if r.Balance < 100 { // $1
		return fmt.Errorf("createAccountRequest: invalid initial amount %d USD cents", r.Balance)
	}
	if r.Name == "" {
		return errors.New("createAccountRequest: missing Name")
	}
	r.Type = strings.ToLower(r.Type)
	switch r.Type {
	case "checking", "savings":
	default:
		return fmt.Errorf("createAccountRequest: unknown Type: %q", r.Type)
	}
	return nil
}

func createAccountNumber() string {
	n, _ := rand.Int(rand.Reader, big.NewInt(1e9))
	return fmt.Sprintf("%d", n.Int64())
}

func createAccount(logger log.Logger, accountRepo accountRepository, transactionRepo transactionRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w, err := wrapResponseWriter(logger, w, r)
		if err != nil {
			return
		}
		requestId := moovhttp.GetRequestId(r)
		if requestId == "" {
			requestId = base.ID()
		}

		var req createAccountRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			logger.Log("accounts", fmt.Sprintf("error reading JSON request: %v", err), "requestId", requestId)
			moovhttp.Problem(w, err)
			return
		}
		if err := req.validate(); err != nil {
			logger.Log("accounts", fmt.Sprintf("error validaing request: %v", err), "requestId", requestId)
			moovhttp.Problem(w, err)
			return
		}

		now := time.Now()
		account := &accounts.Account{
			Id:            base.ID(),
			CustomerId:    req.CustomerId,
			Name:          req.Name,
			AccountNumber: createAccountNumber(),
			RoutingNumber: defaultRoutingNumber,
			Status:        "open",
			Type:          req.Type,
			CreatedAt:     now,
			LastModified:  now,
		}

		if err := accountRepo.CreateAccount(req.CustomerId, account); err != nil {
			logger.Log("accounts", fmt.Sprintf("%v", err), "requestId", requestId)
			moovhttp.Problem(w, err)
			return
		}

		// Submit a transaction of the initial amount (where does the exteranl ABA come from)?
		tx := (&createTransactionRequest{
			Lines: []transactionLine{
				{
					AccountId: account.Id,
					Purpose:   ACHCredit,
					Amount:    req.Balance,
				},
			},
		}).asTransaction(base.ID())
		if err := transactionRepo.createTransaction(tx, createTransactionOpts{InitialDeposit: true}); err != nil {
			logger.Log("accounts", fmt.Errorf("problem creating initial balance transaction: %v", err), "requestId", requestId)
			moovhttp.Problem(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(account)
	}
}
