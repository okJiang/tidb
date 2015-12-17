// Copyright 2015 PingCAP, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// See the License for the specific language governing permissions and
// limitations under the License.

package inspectkv

import (
	"testing"

	. "github.com/pingcap/check"
	"github.com/pingcap/tidb/column"
	"github.com/pingcap/tidb/kv"
	"github.com/pingcap/tidb/meta"
	"github.com/pingcap/tidb/meta/autoid"
	"github.com/pingcap/tidb/model"
	"github.com/pingcap/tidb/mysql"
	"github.com/pingcap/tidb/sessionctx/variable"
	"github.com/pingcap/tidb/store/localstore"
	"github.com/pingcap/tidb/store/localstore/goleveldb"
	"github.com/pingcap/tidb/table"
	"github.com/pingcap/tidb/table/tables"
	"github.com/pingcap/tidb/util/mock"
	"github.com/pingcap/tidb/util/types"
)

func TestT(t *testing.T) {
	TestingT(t)
}

var _ = Suite(&testSuite{})

type testSuite struct {
	store  kv.Storage
	ctx    *mock.Context
	dbInfo *model.DBInfo
	tbInfo *model.TableInfo
}

func (s *testSuite) SetUpSuite(c *C) {
	driver := localstore.Driver{Driver: goleveldb.MemoryDriver{}}
	var err error
	s.store, err = driver.Open("memory:test_inspect")
	c.Assert(err, IsNil)

	s.ctx = mock.NewContext()
	s.ctx.Store = s.store
	variable.BindSessionVars(s.ctx)

	txn, err := s.store.Begin()
	c.Assert(err, IsNil)
	t := meta.NewMeta(txn)

	s.dbInfo = &model.DBInfo{
		ID:   1,
		Name: model.NewCIStr("a"),
	}
	err = t.CreateDatabase(s.dbInfo)
	c.Assert(err, IsNil)

	col := &model.ColumnInfo{
		Name:         model.NewCIStr("c"),
		ID:           0,
		Offset:       0,
		DefaultValue: 1,
		State:        model.StatePublic,
		FieldType:    *types.NewFieldType(mysql.TypeLong),
	}
	col1 := &model.ColumnInfo{
		Name:         model.NewCIStr("c1"),
		ID:           1,
		Offset:       1,
		DefaultValue: 1,
		State:        model.StatePublic,
		FieldType:    *types.NewFieldType(mysql.TypeLong),
	}
	idx := &model.IndexInfo{
		Name:   model.NewCIStr("c"),
		ID:     1,
		Unique: true,
		Columns: []*model.IndexColumn{{
			Name:   model.NewCIStr("c"),
			Offset: 0,
			Length: 255,
		}},
		State: model.StatePublic,
	}
	s.tbInfo = &model.TableInfo{
		ID:      1,
		Name:    model.NewCIStr("t"),
		State:   model.StatePublic,
		Columns: []*model.ColumnInfo{col, col1},
		Indices: []*model.IndexInfo{idx},
	}
	err = t.CreateTable(s.dbInfo.ID, s.tbInfo)
	c.Assert(err, IsNil)

	err = txn.Commit()
	c.Assert(err, IsNil)
}

func (s *testSuite) TearDownSuite(c *C) {
	txn, err := s.store.Begin()
	c.Assert(err, IsNil)
	t := meta.NewMeta(txn)

	err = t.DropTable(s.dbInfo.ID, s.tbInfo.ID)
	c.Assert(err, IsNil)
	err = t.DropDatabase(s.dbInfo.ID)
	c.Assert(err, IsNil)
	err = txn.Commit()
	c.Assert(err, IsNil)

	err = s.store.Close()
	c.Assert(err, IsNil)
}

func (s *testSuite) TestGetDDLInfo(c *C) {
	txn, err := s.store.Begin()
	c.Assert(err, IsNil)
	t := meta.NewMeta(txn)

	owner := &model.Owner{OwnerID: "owner"}
	err = t.SetDDLOwner(owner)
	c.Assert(err, IsNil)
	dbInfo2 := &model.DBInfo{
		ID:    2,
		Name:  model.NewCIStr("b"),
		State: model.StateNone,
	}
	job := &model.Job{
		SchemaID: dbInfo2.ID,
		Type:     model.ActionCreateSchema,
	}
	err = t.EnQueueDDLJob(job)
	c.Assert(err, IsNil)
	info, err := GetDDLInfo(txn)
	c.Assert(err, IsNil)
	c.Assert(info.Owner, DeepEquals, owner)
	c.Assert(info.Job, DeepEquals, job)
	c.Assert(info.ReorgHandle, Equals, int64(0))
	err = txn.Commit()
	c.Assert(err, IsNil)
}

func (s *testSuite) TestScan(c *C) {
	alloc := autoid.NewAllocator(s.store, s.dbInfo.ID)
	tb, err := tables.TableFromMeta(alloc, s.tbInfo)
	c.Assert(err, IsNil)
	indices := tb.Indices()
	_, err = tb.AddRecord(s.ctx, []interface{}{10, 11}, 0)
	c.Assert(err, IsNil)
	s.ctx.FinishTxn(false)

	record1 := &RecordData{Handle: int64(1), Values: []interface{}{int64(10), int64(11)}}
	record2 := &RecordData{Handle: int64(2), Values: []interface{}{int64(20), int64(21)}}
	ver, err := s.store.CurrentVersion()
	c.Assert(err, IsNil)
	records, _, err := ScanSnapshotTableData(s.store, ver, tb, int64(1), 1)
	c.Assert(err, IsNil)
	c.Assert(records, DeepEquals, []*RecordData{record1})

	_, err = tb.AddRecord(s.ctx, record2.Values, record2.Handle)
	c.Assert(err, IsNil)
	s.ctx.FinishTxn(false)
	txn, err := s.store.Begin()
	c.Assert(err, IsNil)

	records, nextHandle, err := ScanTableData(txn, tb, int64(1), 1)
	c.Assert(err, IsNil)
	c.Assert(records, DeepEquals, []*RecordData{record1})
	records, nextHandle, err = ScanTableData(txn, tb, nextHandle, 1)
	c.Assert(err, IsNil)
	c.Assert(records, DeepEquals, []*RecordData{record2})
	startHandle := nextHandle
	records, nextHandle, err = ScanTableData(txn, tb, startHandle, 1)
	c.Assert(records, IsNil)
	c.Assert(nextHandle, Equals, startHandle)
	c.Assert(err, IsNil)

	idxRow1 := &RecordData{Handle: int64(1), Values: []interface{}{int64(10)}}
	idxRow2 := &RecordData{Handle: int64(2), Values: []interface{}{int64(20)}}
	kvIndex := kv.NewKVIndex(tb.IndexPrefix(), indices[0].Name.L, indices[0].ID, indices[0].Unique)
	idxRows, nextVals, err := ScanIndexData(txn, kvIndex, idxRow1.Values, 2)
	c.Assert(err, IsNil)
	c.Assert(idxRows, DeepEquals, []*RecordData{idxRow1, idxRow2})
	idxRows, nextVals, err = ScanIndexData(txn, kvIndex, idxRow1.Values, 1)
	c.Assert(err, IsNil)
	c.Assert(idxRows, DeepEquals, []*RecordData{idxRow1})
	idxRows, nextVals, err = ScanIndexData(txn, kvIndex, nextVals, 1)
	c.Assert(err, IsNil)
	c.Assert(idxRows, DeepEquals, []*RecordData{idxRow2})
	idxRows, nextVals, err = ScanIndexData(txn, kvIndex, nextVals, 1)
	c.Assert(idxRows, IsNil)
	c.Assert(nextVals, DeepEquals, []interface{}{nil})
	c.Assert(err, IsNil)

	s.testTableData(c, tb, []*RecordData{record1, record2})

	s.testIndex(c, tb, tb.Indices()[0])

	err = tb.RemoveRecord(s.ctx, 1, record1.Values)
	c.Assert(err, IsNil)
	err = tb.RemoveRecord(s.ctx, 2, record2.Values)
	c.Assert(err, IsNil)
}

func (s *testSuite) testTableData(c *C, tb table.Table, rs []*RecordData) {
	txn, err := s.store.Begin()
	c.Assert(err, IsNil)

	ret1, ret2, err := DiffTableData(txn, tb, rs, 3, -1)
	c.Assert(err, IsNil)
	c.Assert(ret1, DeepEquals, []*RecordData{nil, nil})
	c.Assert(ret2, DeepEquals, rs)

	ret1, ret2, err = DiffTableData(txn, tb, rs, 1, -1)
	c.Assert(err, IsNil)
	c.Assert(ret1, IsNil)
	c.Assert(ret2, IsNil)

	isEqual, err := EqualTableData(txn, tb, rs, 1, -1)
	c.Assert(err, IsNil)
	c.Assert(isEqual, IsTrue)
	isEqual, err = EqualTableData(txn, tb, rs, 2, -1)
	c.Assert(err, IsNil)
	c.Assert(isEqual, IsFalse)
}

func (s *testSuite) testIndex(c *C, tb table.Table, idx *column.IndexedCol) {
	txn, err := s.store.Begin()
	c.Assert(err, IsNil)

	isEqual, err := EqualIndexData(txn, tb, idx)
	c.Assert(err, IsNil)
	c.Assert(isEqual, IsTrue)

	ret1, ret2, err := DiffIndexData(txn, tb, idx)
	c.Assert(err, IsNil)
	c.Assert(ret1, IsNil)
	c.Assert(ret2, IsNil)

	// current index data:
	// index     data (handle, data): (1, 10), (2, 20), (3, 30)
	// index col data (handle, data): (1, 10), (2, 20), (4, 40)
	err = idx.X.Create(txn, []interface{}{int64(30)}, 3)
	c.Assert(err, IsNil)
	col := tb.Cols()[idx.Columns[0].Offset]
	key := tb.RecordKey(4, col)
	err = tb.SetColValue(txn, key, int64(40))
	c.Assert(err, IsNil)
	err = txn.Commit()
	c.Assert(err, IsNil)

	txn, err = s.store.Begin()
	c.Assert(err, IsNil)
	isEqual, err = EqualIndexData(txn, tb, idx)
	c.Assert(err, IsNil)
	c.Assert(isEqual, IsFalse)
	ret1, ret2, err = DiffIndexData(txn, tb, idx)
	c.Assert(err, IsNil)
	c.Assert(ret1, DeepEquals, []*RecordData{
		{Handle: int64(3), Values: []interface{}{int64(30)}},
		nil,
	})
	c.Assert(ret2, DeepEquals, []*RecordData{
		nil,
		{Handle: int64(4), Values: []interface{}{int64(40)}},
	})

	// current index data:
	// index     data (handle, data): (1, 10), (2, 20), (3, 30), (5, 50), (7, 70), (8, 80)
	// index col data (handle, data): (1, 10), (2, 20), (4, 40), (5, 51), (6, 60), (7, 70)
	err = idx.X.Create(txn, []interface{}{int64(50)}, 5)
	c.Assert(err, IsNil)
	err = idx.X.Create(txn, []interface{}{int64(70)}, 7)
	c.Assert(err, IsNil)
	err = idx.X.Create(txn, []interface{}{int64(80)}, 8)
	c.Assert(err, IsNil)
	col = tb.Cols()[idx.Columns[0].Offset]
	key = tb.RecordKey(5, col)
	err = tb.SetColValue(txn, key, int64(51))
	c.Assert(err, IsNil)
	col = tb.Cols()[idx.Columns[0].Offset]
	key = tb.RecordKey(6, col)
	err = tb.SetColValue(txn, key, int64(60))
	c.Assert(err, IsNil)
	col = tb.Cols()[idx.Columns[0].Offset]
	key = tb.RecordKey(7, col)
	err = tb.SetColValue(txn, key, int64(70))
	c.Assert(err, IsNil)
	err = txn.Commit()
	c.Assert(err, IsNil)

	txn, err = s.store.Begin()
	c.Assert(err, IsNil)
	isEqual, err = EqualIndexData(txn, tb, idx)
	c.Assert(err, IsNil)
	c.Assert(isEqual, IsFalse)
	ret1, ret2, err = DiffIndexData(txn, tb, idx)
	c.Assert(err, IsNil)
	c.Assert(ret1, DeepEquals, []*RecordData{
		{Handle: int64(3), Values: []interface{}{int64(30)}},
		nil,
		{Handle: int64(5), Values: []interface{}{int64(50)}},
		nil,
		{Handle: int64(8), Values: []interface{}{int64(80)}},
	})
	c.Assert(ret2, DeepEquals, []*RecordData{
		nil,
		{Handle: int64(4), Values: []interface{}{int64(40)}},
		{Handle: int64(5), Values: []interface{}{int64(51)}},
		{Handle: int64(6), Values: []interface{}{int64(60)}},
		nil,
	})

	// current index data:
	// index     data (handle, data): (1, 10), (2, 20), (3, 30), (5, 50), (7, 70), (8, 80)
	// index col data (handle, data): (1, 10), (2, 20), (4, 40), (5, 51), (6, 60), (7, 70), (9, 90)
	col = tb.Cols()[idx.Columns[0].Offset]
	key = tb.RecordKey(9, col)
	err = tb.SetColValue(txn, key, int64(90))
	c.Assert(err, IsNil)
	err = txn.Commit()
	c.Assert(err, IsNil)

	txn, err = s.store.Begin()
	c.Assert(err, IsNil)
	isEqual, err = EqualIndexData(txn, tb, idx)
	c.Assert(err, IsNil)
	c.Assert(isEqual, IsFalse)
	ret1, ret2, err = DiffIndexData(txn, tb, idx)
	c.Assert(err, IsNil)
	c.Assert(ret1, DeepEquals, []*RecordData{
		{Handle: int64(3), Values: []interface{}{int64(30)}},
		nil,
		{Handle: int64(5), Values: []interface{}{int64(50)}},
		nil,
		{Handle: int64(8), Values: []interface{}{int64(80)}},
		nil,
	})
	c.Assert(ret2, DeepEquals, []*RecordData{
		nil,
		{Handle: int64(4), Values: []interface{}{int64(40)}},
		{Handle: int64(5), Values: []interface{}{int64(51)}},
		{Handle: int64(6), Values: []interface{}{int64(60)}},
		nil,
		{Handle: int64(9), Values: []interface{}{int64(90)}},
	})
}
