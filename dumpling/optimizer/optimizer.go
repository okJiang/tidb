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

package optimizer

import (
	"strings"

	"github.com/juju/errors"
	"github.com/pingcap/tidb/ast"
	"github.com/pingcap/tidb/context"
	"github.com/pingcap/tidb/infoschema"
	"github.com/pingcap/tidb/optimizer/plan"
)

// Optimize do optimization and create a Plan.
// InfoSchema has to be passed in as parameter because
// it can not be changed after binding.
func Optimize(is infoschema.InfoSchema, ctx context.Context, node ast.Node) (plan.Plan, error) {
	if err := validate(node); err != nil {
		return nil, errors.Trace(err)
	}
	if err := bindInfo(node, is, ctx); err != nil {
		return nil, errors.Trace(err)
	}
	if err := computeType(node); err != nil {
		return nil, errors.Trace(err)
	}
	if err := rewriteStatic(ctx, node); err != nil {
		return nil, errors.Trace(err)
	}
	p := buildPlan(node)
	alts := alternatives(p)

	refine(ctx, p)
	bestCost := plan.EstimateCost(p)
	bestPlan := p

	for _, alt := range alts {
		refine(ctx, alt)
		cost := plan.EstimateCost(alt)
		if cost < bestCost {
			bestCost = cost
			bestPlan = alt
		}
	}
	return bestPlan, nil
}

type supportChecker struct {
	unsupported bool
}

func (c *supportChecker) Enter(in ast.Node) (ast.Node, bool) {
	switch in.(type) {
	case *ast.SubqueryExpr, *ast.AggregateFuncExpr, *ast.GroupByClause, *ast.HavingClause, *ast.ParamMarkerExpr:
		c.unsupported = true
	case *ast.Join:
		x := in.(*ast.Join)
		if x.Right != nil {
			c.unsupported = true
		} else {
			ts, tsok := x.Left.(*ast.TableSource)
			if !tsok {
				c.unsupported = true
			} else {
				tn, tnok := ts.Source.(*ast.TableName)
				if !tnok {
					c.unsupported = true
				} else if strings.EqualFold(tn.Schema.O, infoschema.Name) {
					c.unsupported = true
				}
			}
		}
	case *ast.SelectStmt:
		x := in.(*ast.SelectStmt)
		if x.Distinct {
			c.unsupported = true
		}
	}
	return in, c.unsupported
}

func (c *supportChecker) Leave(in ast.Node) (ast.Node, bool) {
	return in, !c.unsupported
}

// Supported checks if the node is supported to use new plan.
// We first support single table select statement without group by clause or aggregate functions.
// TODO: 1. insert/update/delete. 2. join tables. 3. subquery. 4. group by and aggregate function.
func Supported(node ast.Node) bool {
	if _, ok := node.(*ast.SelectStmt); !ok {
		return false
	}
	var checker supportChecker
	node.Accept(&checker)
	return !checker.unsupported
	//		return false
}
