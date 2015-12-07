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

package plan

import (
	"github.com/ngaut/log"
)

// Alternatives returns multiple alternative plans that
// can be picked base on its cost.
func Alternatives(p Plan) []Plan {
	var plans []Plan
	switch x := p.(type) {
	case nil:
	case *TableScan:
		plans = tableScanAlternatives(x)
	case WithSrcPlan:
		plans = planWithSrcAlternatives(x)
	default:
		log.Fatalf("unknown plan %T", p)
	}
	for _, val := range plans {
		refine(val)
	}
	return plans
}

// tableScanAlternatives returns all index plans from the same table.
func tableScanAlternatives(p *TableScan) []Plan {
	var alts []Plan
	for _, v := range p.Table.Indices {
		fullRange := &IndexRange{
			LowVal:  []interface{}{nil},
			HighVal: []interface{}{MaxVal},
		}
		ip := &IndexScan{
			Index:  v,
			Table:  p.Table,
			Ranges: []*IndexRange{fullRange},
		}
		ip.SetFields(p.Fields())
		alts = append(alts, ip)
	}
	return alts
}

// planWithSrcAlternatives shallow copies the WithSrcPlan,
// and set its src to src alternatives.
func planWithSrcAlternatives(p WithSrcPlan) []Plan {
	srcs := Alternatives(p.Src())
	for i, val := range srcs {
		alt := shallowCopy(p)
		alt.SetSrc(val)
		srcs[i] = alt
	}
	return srcs
}

func shallowCopy(p WithSrcPlan) WithSrcPlan {
	var copied WithSrcPlan
	switch x := p.(type) {
	case *Filter:
		n := *x
		copied = &n
	case *SelectLock:
		n := *x
		copied = &n
	case *SelectFields:
		n := *x
		copied = &n
	case *Sort:
		n := *x
		copied = &n
	case *Limit:
		n := *x
		copied = &n
	}
	return copied
}
