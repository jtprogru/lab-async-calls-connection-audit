package main

import (
	"context"
	"fmt"
	"net/http"

	"example.com/orders/internal/checkout"
	"example.com/orders/internal/inventory"
	"example.com/orders/internal/payments"
)

func handleOrder(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := inventory.Reserve(id, 1); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	if err := payments.Charge(r.Context(), id); err != nil {
		go checkout.Refund(id) // fire-and-forget: ошибку никто не увидит
		http.Error(w, "payment failed", http.StatusPaymentRequired)
		return
	}
	go notify(context.Background(), id) // отвязан от запроса намеренно?
	w.WriteHeader(http.StatusCreated)
}

func notify(ctx context.Context, id string) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://notifier.internal/order/"+id, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Printf("notify %s: %v\n", id, err)
		return
	}
	defer resp.Body.Close()
}

func main() {
	http.HandleFunc("POST /orders/{id}", handleOrder)
	http.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "ok\n")
	})
	if err := http.ListenAndServe(":8080", nil); err != nil {
		panic(err)
	}
}
