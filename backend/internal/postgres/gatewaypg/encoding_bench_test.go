package gatewaypg

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// Isolate pgx's real parameter encoder from the network and database. Payload
// lengths straddle buffer-growth boundaries, exposing extra full-buffer copies.
func BenchmarkFinishCallEncoding(b *testing.B) {
	for _, lengths := range [][2]int{{400, 620}, {500, 860}, {600, 800}} {
		b.Run(fmt.Sprintf("attempts%d-snapshot%d", lengths[0], lengths[1]), func(b *testing.B) {
			db := &encodingDB{types: pgtype.NewMap()}
			q := New(db)
			ctx := context.Background()
			id := "11111111-1111-4111-8111-111111111111"
			mode, source := "cheapest", "default"
			ttft, duration := int32(52), int32(60)
			speed := float64(1000)
			size := int64(400)
			responseID := "resp_123456789"
			arg := FinishCallParams{ID: id, Outcome: "succeeded", ModelID: &mode, FinalChannelID: &id, RoutingMode: &mode, RoutingSource: &source,
				Attempts: []byte("[]" + strings.Repeat(" ", lengths[0]-2)), PriceSnapshot: []byte("{}" + strings.Repeat(" ", lengths[1]-2)),
				InputTokens: 50, OutputTokens: 64, CostNano: 114000, FeeNano: 114, TtftMs: &ttft, DurationMs: &duration, OutputTokensPerSecond: &speed, ResponseBytes: &size, UpstreamResponseID: &responseID, Diagnostics: []byte("{}")}
			if _, err := q.FinishCall(ctx, arg); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if _, err := q.FinishCall(ctx, arg); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

type encodingDB struct {
	types     *pgtype.Map
	builder   pgx.ExtendedQueryBuilder
	statement pgconn.StatementDescription
}

func (d *encodingDB) Exec(_ context.Context, _ string, args ...any) (pgconn.CommandTag, error) {
	if d.statement.ParamOIDs == nil {
		for _, arg := range args {
			oid := uint32(pgtype.TextOID)
			if _, ok := arg.([]byte); ok {
				oid = pgtype.JSONBOID
			} else if arg != nil {
				typ := reflect.TypeOf(arg)
				if typ.Kind() == reflect.Pointer {
					typ = typ.Elem()
				}
				switch typ.Kind() {
				case reflect.Int32:
					oid = pgtype.Int4OID
				case reflect.Int64:
					oid = pgtype.Int8OID
				case reflect.Float64:
					oid = pgtype.Float8OID
				}
			}
			d.statement.ParamOIDs = append(d.statement.ParamOIDs, oid)
		}
	}
	err := d.builder.Build(d.types, &d.statement, args)
	return pgconn.NewCommandTag("UPDATE 1"), err
}
func (*encodingDB) Query(context.Context, string, ...any) (pgx.Rows, error) { panic("not used") }
func (*encodingDB) QueryRow(context.Context, string, ...any) pgx.Row        { panic("not used") }
