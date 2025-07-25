package tests

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/transferia/transferia/internal/logger"
	"github.com/transferia/transferia/pkg/abstract"
	"github.com/transferia/transferia/pkg/abstract/model"
	"github.com/transferia/transferia/pkg/providers/postgres"
	"github.com/transferia/transferia/pkg/providers/postgres/pgrecipe"
)

func TestShardingStorage_ShardTable(t *testing.T) {
	_ = pgrecipe.RecipeSource(pgrecipe.WithPrefix(""), pgrecipe.WithInitDir("test_scripts"))
	srcPort, _ := strconv.Atoi(os.Getenv("PG_LOCAL_PORT"))
	v := &postgres.PgSource{
		Hosts:    []string{"localhost"},
		User:     os.Getenv("PG_LOCAL_USER"),
		Password: model.SecretString(os.Getenv("PG_LOCAL_PASSWORD")),
		Database: os.Getenv("PG_LOCAL_DATABASE"),
		Port:     srcPort,
		SlotID:   "testslot",
	}
	v.WithDefaults()
	require.NotEqual(t, 0, v.DesiredTableSize)
	storage, err := postgres.NewStorage(v.ToStorageParams(nil))
	require.NoError(t, err)
	ctx := context.Background()
	storage.Config.DesiredTableSize = 1 * 1024 * 1024
	err = storage.BeginPGSnapshot(context.TODO())
	require.NoError(t, err)
	logger.Log.Infof("create snapshot: %v, ts: %v", storage.ShardedStateLSN, storage.ShardedStateTS)
	_, err = storage.Conn.Exec(ctx, "delete from __test_to_shard where 1 = 1;")
	require.NoError(t, err)
	t.Run("sharded state", func(t *testing.T) {
		snapshotCtime := storage.ShardedStateTS
		lsn := storage.ShardedStateLSN
		require.NotEmpty(t, snapshotCtime, "Snapshot timestamp is not set!")
		require.NotEmpty(t, lsn, "Snapshot lsn is not set!")
		cont, err := storage.ShardingContext()
		require.NoError(t, err)
		err = storage.SetShardingContext(cont)
		require.NoError(t, err)
		require.NotEmpty(t, storage.ShardedStateLSN,
			"Snapshot lsn is not set from sharding context!")
		require.NotEmpty(t, storage.ShardedStateTS,
			"Snapshot timestamp is not set from sharding context!")
		require.Equal(t, lsn, storage.ShardedStateLSN,
			"Snapshot lsn from sharding context differs from original!")
		require.Equal(t, snapshotCtime.In(time.UTC), storage.ShardedStateTS.In(time.UTC),
			"Snapshot timestamp from sharding context differs from original!")

	})
	t.Run("bigserial", func(t *testing.T) {
		tables, err := storage.ShardTable(ctx, abstract.TableDescription{
			Name:   "__test_to_shard",
			Schema: "public",
			Filter: "",
			EtaRow: 0,
			Offset: 0,
		})
		require.NoError(t, err)
		require.Len(t, tables, 4)
		var res []abstract.ChangeItem
		for _, tbl := range tables {
			require.NoError(t, storage.LoadTable(ctx, tbl, func(input []abstract.ChangeItem) error {
				for _, row := range input {
					if row.IsRowEvent() {
						res = append(res, row)
					}
				}
				return nil
			}))
		}
		require.Len(t, res, 100_000)
	})
	t.Run("Shard by specific field", func(t *testing.T) {
		keysMap := map[string][]string{"__test_to_shard": {"text"}}
		storage.Config.ShardingKeyFields = keysMap
		tables, err := storage.ShardTable(ctx, abstract.TableDescription{
			Name:   "__test_to_shard",
			Schema: "public",
			Filter: "",
			EtaRow: 0,
			Offset: 0,
		})
		require.NoError(t, err)
		require.Len(t, tables, 4)
		require.True(t, strings.Contains(string(tables[0].Filter), "row(\"text\")::text"))
		var res []abstract.ChangeItem
		for _, tbl := range tables {
			require.NoError(t, storage.LoadTable(ctx, tbl, func(input []abstract.ChangeItem) error {
				for _, row := range input {
					if row.IsRowEvent() {
						res = append(res, row)
					}
				}
				return nil
			}))
		}
		require.Len(t, res, 100_000)
	})
	t.Run("not so big serial", func(t *testing.T) {
		tables, err := storage.ShardTable(ctx, abstract.TableDescription{
			Name:   "__test_to_shard_int32",
			Schema: "public",
			Filter: "",
			EtaRow: 0,
			Offset: 0,
		})
		require.NoError(t, err)
		require.Len(t, tables, 4)
		require.Contains(t, string(tables[0].Filter), "\"Id\" < '25000'")
		require.Contains(t, string(tables[1].Filter), "\"Id\" >= '25000' AND \"Id\" < '50000'")
		require.Contains(t, string(tables[2].Filter), "\"Id\" >= '50000' AND \"Id\" < '75000'")
		require.Contains(t, string(tables[3].Filter), "\"Id\" >= '75000'")
		var res []abstract.ChangeItem
		for _, tbl := range tables {
			require.NoError(t, storage.LoadTable(ctx, tbl, func(input []abstract.ChangeItem) error {
				for _, row := range input {
					if row.IsRowEvent() {
						res = append(res, row)
					}
				}
				return nil
			}))
		}
		require.Len(t, res, 100_000)
	})
	t.Run("all keys are numeric", func(t *testing.T) {
		tables, err := storage.ShardTable(ctx, abstract.TableDescription{
			Name:   "__test_all_keys_are_numeric",
			Schema: "public",
			Filter: "",
			EtaRow: 0,
			Offset: 0,
		})
		require.NoError(t, err)
		require.Len(t, tables, 4)
		filter := "abs((\"id\"+\"bigserial_key\"+\"numeric_key\"+\"bigint_key\"+\"float_key\"+\"double_key\"+\"smallint_key\"+\"integer_key\"+\"real_key\")::bigint % 4)"
		require.Contains(t, string(tables[0].Filter), filter+" = 0")
		require.Contains(t, string(tables[1].Filter), filter+" = 1")
		require.Contains(t, string(tables[2].Filter), filter+" = 2")
		require.Contains(t, string(tables[3].Filter), filter+" = 3")
		var res []abstract.ChangeItem
		for _, tbl := range tables {
			require.NoError(t, storage.LoadTable(ctx, tbl, func(input []abstract.ChangeItem) error {
				for _, row := range input {
					if row.IsRowEvent() {
						res = append(res, row)
					}
				}
				return nil
			}))
		}
		require.Len(t, res, 100_000)
	})
	t.Run("text pk", func(t *testing.T) {
		tables, err := storage.ShardTable(ctx, abstract.TableDescription{
			Name:   "__test_text_pk",
			Schema: "public",
			Filter: "",
			EtaRow: 0,
			Offset: 0,
		})
		require.NoError(t, err)
		require.Len(t, tables, 4)
		filter := "abs(hashtext(row(\"serial_key\",\"txt\")::text) % 4)"
		require.Contains(t, string(tables[0].Filter), filter+" = 0")
		require.Contains(t, string(tables[1].Filter), filter+" = 1")
		require.Contains(t, string(tables[2].Filter), filter+" = 2")
		require.Contains(t, string(tables[3].Filter), filter+" = 3")
		var res []abstract.ChangeItem
		for _, tbl := range tables {
			require.NoError(t, storage.LoadTable(ctx, tbl, func(input []abstract.ChangeItem) error {
				for _, row := range input {
					if row.IsRowEvent() {
						res = append(res, row)
					}
				}
				return nil
			}))
		}
		require.Len(t, res, 100_000)
	})
}

func TestShardingStorage_CalculatePartsCount(t *testing.T) {
	partCount := postgres.CalculatePartCount(101, 100, 4)
	require.Equal(t, partCount, uint64(2))

	partCount = postgres.CalculatePartCount(101, 100, 1)
	require.Equal(t, partCount, uint64(1))

	partCount = postgres.CalculatePartCount(100, 100, 4)
	require.Equal(t, partCount, uint64(1))

	partCount = postgres.CalculatePartCount(1001, 100, 4)
	require.Equal(t, partCount, uint64(4))
}
