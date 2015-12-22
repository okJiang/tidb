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
	"io"
	"reflect"
	"strings"

	"github.com/juju/errors"
	"github.com/pingcap/tidb/column"
	"github.com/pingcap/tidb/kv"
	"github.com/pingcap/tidb/meta"
	"github.com/pingcap/tidb/model"
	"github.com/pingcap/tidb/table"
	"github.com/pingcap/tidb/table/tables"
	"github.com/pingcap/tidb/terror"
)

// DDLInfo is for DDL information.
type DDLInfo struct {
	SchemaVer   int64
	ReorgHandle int64
	Owner       *model.Owner
	Job         *model.Job
}

// GetDDLInfo returns DDL information.
func GetDDLInfo(txn kv.Transaction) (*DDLInfo, error) {
	var err error
	info := &DDLInfo{}
	t := meta.NewMeta(txn)

	info.Owner, err = t.GetDDLOwner()
	if err != nil {
		return nil, errors.Trace(err)
	}
	info.Job, err = t.GetDDLJob(0)
	if err != nil {
		return nil, errors.Trace(err)
	}
	info.SchemaVer, err = t.GetSchemaVersion()
	if err != nil {
		return nil, errors.Trace(err)
	}
	if info.Job == nil {
		return info, nil
	}

	info.ReorgHandle, err = t.GetDDLReorgHandle(info.Job)
	if err != nil {
		return nil, errors.Trace(err)
	}

	return info, nil
}

func nextIndexVals(data []interface{}) []interface{} {
	// Add 0x0 to the end of data.
	return append(data, nil)
}

// RecordData is the record data composed of a handle and values.
type RecordData struct {
	Handle int64
	Values []interface{}
}

// GetIndexRecordsCount returns the total number of the index records from startVals.
// If startVals = nil, returns the total number of the index records.
func GetIndexRecordsCount(txn kv.Transaction, kvIndex kv.Index, startVals []interface{}) (int64, error) {
	it, _, err := kvIndex.Seek(txn, startVals)
	if err != nil {
		return 0, errors.Trace(err)
	}
	defer it.Close()

	var cnt int64
	for {
		_, _, err := it.Next()
		if terror.ErrorEqual(err, io.EOF) {
			break
		} else if err != nil {
			return 0, errors.Trace(err)
		}
		cnt++
	}

	return cnt, nil
}

// ScanIndexData scans the index handles and values in a limited number, according to the index information.
// It returns data and the next startVals until it doesn't have data, then returns data is nil and
// the next startVals is the values which can't get data.
// If limit = -1, it returns the index data of the whole.
func ScanIndexData(txn kv.Transaction, kvIndex kv.Index, startVals []interface{}, limit int64) (
	[]*RecordData, []interface{}, error) {
	it, _, err := kvIndex.Seek(txn, startVals)
	if err != nil {
		return nil, nil, errors.Trace(err)
	}
	defer it.Close()

	var idxRows []*RecordData
	var curVals []interface{}
	for limit != 0 {
		val, h, err1 := it.Next()
		if terror.ErrorEqual(err1, io.EOF) {
			return idxRows, nextIndexVals(curVals), nil
		} else if err1 != nil {
			return nil, nil, errors.Trace(err1)
		}
		idxRows = append(idxRows, &RecordData{Handle: h, Values: val})
		limit--
		curVals = val
	}

	nextVals, _, err := it.Next()
	if terror.ErrorEqual(err, io.EOF) {
		return idxRows, nextIndexVals(curVals), nil
	} else if err != nil {
		return nil, nil, errors.Trace(err)
	}

	return idxRows, nextVals, nil
}

// ScanIndexColData scans the index handles and values in a limited number, according to the corresponding column.
// It returns data and the next startHandle until it doesn't have data, then returns data is nil and
// the next startHandle is the handle which can't get data.
// If limit = -1, it returns the index data of the whole.
func ScanIndexColData(txn kv.Transaction, t table.Table, idx *column.IndexedCol, startHandle, limit int64) (
	[]*RecordData, int64, error) {
	cols := make([]*column.Col, len(idx.Columns))
	for i, col := range idx.Columns {
		cols[i] = t.Cols()[col.Offset]
	}

	return scanTableData(txn, t, cols, startHandle, limit)
}

// CompareIndexData compares index data one by one.
// It returns nil if the data from the index is equal to the data from the table columns,
// otherwise it returns an error with a different set of records.
func CompareIndexData(txn kv.Transaction, t table.Table, idx *column.IndexedCol) error {
	err := checkIndexAndCols(txn, t, idx)
	if err != nil {
		return errors.Trace(err)
	}

	return checkColsAndIndex(txn, t, idx)
}

func checkIndexAndCols(txn kv.Transaction, t table.Table, idx *column.IndexedCol) error {
	kvIndex := kv.NewKVIndex(t.IndexPrefix(), idx.Name.L, idx.ID, idx.Unique)
	it, err := kvIndex.SeekFirst(txn)
	if err != nil {
		return errors.Trace(err)
	}
	defer it.Close()

	cols := make([]*column.Col, len(idx.Columns))
	for i, col := range idx.Columns {
		cols[i] = t.Cols()[col.Offset]
	}

	for {
		vals1, h, err := it.Next()
		if terror.ErrorEqual(err, io.EOF) {
			break
		} else if err != nil {
			return errors.Trace(err)
		}

		vals2, err := t.RowWithCols(txn, h, cols)
		if terror.ErrorEqual(err, kv.ErrNotExist) {
			record := &RecordData{Handle: h, Values: vals1}
			err = errors.Errorf("index:%v != record:%v", record, nil)
		}
		if err != nil {
			return errors.Trace(err)
		}
		if !reflect.DeepEqual(vals1, vals2) {
			record1 := &RecordData{Handle: h, Values: vals1}
			record2 := &RecordData{Handle: h, Values: vals2}
			return errors.Errorf("index:%v != record:%v", record1, record2)
		}
	}

	return nil
}

func checkColsAndIndex(txn kv.Transaction, t table.Table, idx *column.IndexedCol) error {
	cols := make([]*column.Col, len(idx.Columns))
	for i, col := range idx.Columns {
		cols[i] = t.Cols()[col.Offset]
	}

	startKey := t.RecordKey(0, nil)
	kvIndex := kv.NewKVIndex(t.IndexPrefix(), idx.Name.L, idx.ID, idx.Unique)
	err := t.IterRecords(txn, string(startKey), cols,
		func(h1 int64, vals1 []interface{}, cols []*column.Col) (bool, error) {
			isExist, h2, err := kvIndex.Exist(txn, vals1, h1)
			if terror.ErrorEqual(err, terror.ErrKeyExists) {
				record1 := &RecordData{Handle: h1, Values: vals1}
				record2 := &RecordData{Handle: h2, Values: vals1}
				return false, errors.Errorf("index:%v != record:%v", record2, record1)
			}
			if err != nil {
				return false, errors.Trace(err)
			}
			if !isExist {
				record := &RecordData{Handle: h1, Values: vals1}
				return false, errors.Errorf("index:%v != record:%v", nil, record)
			}

			return true, nil
		})

	if err != nil {
		return errors.Trace(err)
	}

	return nil
}

func scanTableData(retriever kv.Retriever, t table.Table, cols []*column.Col, startHandle, limit int64) (
	[]*RecordData, int64, error) {
	var records []*RecordData

	startKey := t.RecordKey(startHandle, nil)
	err := t.IterRecords(retriever, string(startKey), cols,
		func(h int64, d []interface{}, cols []*column.Col) (bool, error) {
			if limit != 0 {
				r := &RecordData{
					Handle: h,
					Values: d,
				}
				records = append(records, r)
				limit--
				return true, nil
			}

			return false, nil
		})
	if err != nil {
		return nil, 0, errors.Trace(err)
	}

	if len(records) == 0 {
		return records, startHandle, nil
	}

	nextHandle := records[len(records)-1].Handle + 1

	return records, nextHandle, nil
}

// ScanTableData scans table row handles and column values in a limited number.
// It returns data and the next startHandle until it doesn't have data, then returns data is nil and
// the next startHandle is the handle which can't get data.
// If limit = -1, it returns the table data of the whole.
func ScanTableData(retriever kv.Retriever, t table.Table, startHandle, limit int64) (
	[]*RecordData, int64, error) {
	return scanTableData(retriever, t, t.Cols(), startHandle, limit)
}

// ScanSnapshotTableData scans the ver version of the table data in a limited number.
// It returns data and the next startHandle until it doesn't have data, then returns data is nil and
// the next startHandle is the handle which can't get data.
// If limit = -1, it returns the table data of the whole.
func ScanSnapshotTableData(store kv.Storage, ver kv.Version, t table.Table, startHandle, limit int64) (
	[]*RecordData, int64, error) {
	snap, err := store.GetSnapshot(ver)
	if err != nil {
		return nil, 0, errors.Trace(err)
	}
	defer snap.Release()

	records, nextHandle, err := ScanTableData(snap, t, startHandle, limit)

	return records, nextHandle, errors.Trace(err)
}

// CompareTableData compares data and the corresponding table data one by one.
// It returns nil if data is equal to the data that scans from table, otherwise
// it returns an error with a different set of records. If exact is false, only
// compares handle.
func CompareTableData(txn kv.Transaction, t table.Table, data []*RecordData, exact bool) error {
	var err error
	var vals []interface{}

	for _, r := range data {
		vals, err = t.RowWithCols(txn, r.Handle, t.Cols())
		if terror.ErrorEqual(err, kv.ErrNotExist) {
			record1 := &RecordData{Handle: r.Handle, Values: r.Values}
			err = errors.Errorf("data:%v != record:%v", record1, nil)
		}
		if err != nil {
			break
		}

		if !exact {
			continue
		}
		if !reflect.DeepEqual(r.Values, vals) {
			record1 := &RecordData{Handle: r.Handle, Values: r.Values}
			record2 := &RecordData{Handle: r.Handle, Values: vals}
			err = errors.Errorf("data:%v != record:%v", record1, record2)
			break
		}
	}
	if err != nil {
		return errors.Trace(err)
	}

	startKey := t.RecordKey(0, nil)
	filterFunc := func(h int64, vals []interface{}, cols []*column.Col) (bool, error) {
		for _, r := range data {
			if !exact {
				if r.Handle == h {
					return true, nil
				}
				continue
			}
			if r.Handle == h && reflect.DeepEqual(r.Values, vals) {
				return true, nil
			}
		}
		record := &RecordData{Handle: h, Values: vals}
		return false, errors.Errorf("data:%v != record:%v", nil, record)
	}
	err = t.IterRecords(txn, string(startKey), t.Cols(), filterFunc)
	if err != nil {
		return errors.Trace(err)
	}

	return nil
}

// GetTableRecordsCount returns the total number of table records from startHandle.
// If startHandle = 0, returns the total number of table records.
func GetTableRecordsCount(txn kv.Transaction, t table.Table, startHandle int64) (int64, error) {
	startKey := t.RecordKey(startHandle, nil)
	it, err := txn.Seek(startKey)
	if err != nil {
		return 0, errors.Trace(err)
	}

	var cnt int64
	prefix := t.KeyPrefix()
	for it.Valid() && strings.HasPrefix(it.Key(), prefix) {
		handle, err := tables.DecodeRecordKeyHandle(it.Key())
		if err != nil {
			return 0, errors.Trace(err)
		}

		it.Close()
		rk := t.RecordKey(handle+1, nil)
		it, err = txn.Seek(rk)
		if err != nil {
			return 0, errors.Trace(err)
		}

		cnt++
	}

	it.Close()

	return cnt, nil
}
