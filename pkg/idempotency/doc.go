// Package idempotency is the platform's one REST idempotency (audit-fix campaign
// spec §3.2). It replaces the five hand-written variants (middleware around the
// handler, "in progress" rows with a TTL, stored response bodies).
//
// The rules, and what enforces each:
//
//   - The key is stored in the SAME transaction as the work. There is no "in
//     progress" row: a request that has not committed has left nothing, and one
//     that rolled back leaves nothing. Claim.Run takes the transaction (pgx.Tx),
//     so it cannot be called outside one.
//   - One row per (caller, endpoint, key) with a hash of the request body. The
//     same key with the same body returns the first result; with a different body
//     it is refused, 409 COMMON_IDEMPOTENCY_KEY_REUSED.
//   - Two identical requests at once: the second waits on the unique index until
//     the first commits (it then gets the first result) or rolls back (it then
//     runs).
//   - The row names the record the first request created — resource type and id —
//     never the response text. A replay loads that record and passes it through
//     the normal permission check, so a replay can never show more than a fresh
//     read would.
//   - Rows older than 24 hours are deleted by the outbox reaper
//     (OUTBOX_IDEMPOTENCY_RETENTION).
//
// # Use
//
// Register the write route with Add (or Post/Put/Patch/Delete), which puts the
// Require middleware in front of the handler; read the claim in the handler;
// pass it to the application method that creates the record, and run the work
// through it inside the transaction:
//
//	idempotency.Post(g, "/orders", h.CreateOrder)
//
//	func (h *Handler) CreateOrder(c echo.Context) error {
//		claim, err := idempotency.ClaimFor(c, userID.String())
//		if err != nil {
//			return response.JSONError(c, err)
//		}
//		res, err := h.svc.Create(c.Request().Context(), claim, cmd)
//		...
//	}
//
//	func (s *Service) Create(ctx context.Context, claim idempotency.Claim, cmd Cmd) (*Order, error) {
//		var out *Order
//		err := s.tx.RunInTx(ctx, func(tx outbox.Tx) error {
//			pgxTx, _ := outbox.TxAsPgx(tx)
//			outcome, err := claim.Run(ctx, pgxTx, func(ctx context.Context) (idempotency.Resource, error) {
//				order, err := s.createOrder(ctx, tx, cmd) // the real work, in tx
//				if err != nil {
//					return idempotency.Resource{}, err
//				}
//				out = order
//				return idempotency.Resource{Type: "order", ID: order.ID.String()}, nil
//			})
//			if err != nil {
//				return err
//			}
//			if outcome.Replayed {
//				out, err = s.loadForCaller(ctx, tx, outcome.Resource.ID, cmd.Caller) // permission check
//			}
//			return err
//		})
//		return out, err
//	}
//
// # Optional keys, large bodies, multipart
//
// Required is the default. For an endpoint whose shipped clients send no key,
// idempotency.PostOptional (and the Put/Patch/Delete/Add forms) make the key
// optional: an absent header runs the request once with no idempotency (a keyless
// Claim whose Run only runs the work), a present one is enforced as usual, and
// idempotency_keyless_requests_total{route} counts the keyless calls until the
// route can become required. A keyed body is capped at 1 MiB; an upload route
// raises it with idempotency.With(idempotency.WithMaxBody(10<<20)).Post(...) (the
// body is held in memory once, so the peak is the cap times concurrent uploads),
// and a multipart route whose clients retry opts into
// idempotency.WithMultipartHash(), which ignores the multipart boundary.
//
// An application method that creates a record takes an idempotency.Claim, which
// only ClaimFor / NewClaim can build, so the compiler refuses a create path with
// no idempotency; CheckRoutes (one test per service) refuses a write route that
// was not registered through Add; the lint config bans reading the
// Idempotency-Key header or keeping idempotency tables by hand.
package idempotency
