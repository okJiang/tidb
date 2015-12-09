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
	"fmt"
	"testing"

	"github.com/ngaut/log"
	. "github.com/pingcap/check"
	"github.com/pingcap/tidb/ast"
	"github.com/pingcap/tidb/model"
	"github.com/pingcap/tidb/parser"
)

var _ = Suite(&testPlanSuite{})

func TestT(t *testing.T) {
	TestingT(t)
}

type testPlanSuite struct{}

func (s *testPlanSuite) TestRangeBuilder(c *C) {
	log.SetLevel(log.LOG_LEVEL_ERROR)
	rb := &rangeBuilder{}

	cases := []struct {
		exprStr   string
		resultStr string
	}{
		{
			exprStr:   "a = 1",
			resultStr: "[[1 1]]",
		},
		{
			exprStr:   "1 = a",
			resultStr: "[[1 1]]",
		},
		{
			exprStr:   "a != 1",
			resultStr: "[[-inf 1) (1 +inf]]",
		},
		{
			exprStr:   "1 != a",
			resultStr: "[[-inf 1) (1 +inf]]",
		},
		{
			exprStr:   "a > 1",
			resultStr: "[(1 +inf]]",
		},
		{
			exprStr:   "1 < a",
			resultStr: "[(1 +inf]]",
		},
		{
			exprStr:   "a >= 1",
			resultStr: "[[1 +inf]]",
		},
		{
			exprStr:   "1 <= a",
			resultStr: "[[1 +inf]]",
		},
		{
			exprStr:   "a < 1",
			resultStr: "[[-inf 1)]",
		},
		{
			exprStr:   "1 > a",
			resultStr: "[[-inf 1)]",
		},
		{
			exprStr:   "a <= 1",
			resultStr: "[[-inf 1]]",
		},
		{
			exprStr:   "1 >= a",
			resultStr: "[[-inf 1]]",
		},
		{
			exprStr:   "(a)",
			resultStr: "[[-inf 0) (0 +inf]]",
		},
		{
			exprStr:   "a in (1, 3, NULL, 2)",
			resultStr: "[[<nil> <nil>] [1 1] [2 2] [3 3]]",
		},
		{
			exprStr:   "a between 1 and 2",
			resultStr: "[[1 2]]",
		},
		{
			exprStr:   "a not between 1 and 2",
			resultStr: "[[-inf 1) (2 +inf]]",
		},
		{
			exprStr:   "a not between null and 0",
			resultStr: "[(0 +inf]]",
		},
		{
			exprStr:   "a between 2 and 1",
			resultStr: "[]",
		},
		{
			exprStr:   "a not between 2 and 1",
			resultStr: "[[-inf +inf]]",
		},
		{
			exprStr:   "a IS NULL",
			resultStr: "[[<nil> <nil>]]",
		},
		{
			exprStr:   "a IS NOT NULL",
			resultStr: "[[-inf +inf]]",
		},
		{
			exprStr:   "a IS TRUE",
			resultStr: "[[-inf 0) (0 +inf]]",
		},
		{
			exprStr:   "a IS NOT TRUE",
			resultStr: "[[<nil> <nil>] [0 0]]",
		},
		{
			exprStr:   "a IS FALSE",
			resultStr: "[[0 0]]",
		},
		{
			exprStr:   "a IS NOT FALSE",
			resultStr: "[[<nil> 0) (0 +inf]]",
		},
		{
			exprStr:   "a LIKE 'abc%'",
			resultStr: "[[abc abd)]",
		},
		{
			exprStr:   "a LIKE 'abc_'",
			resultStr: "[(abc abd)]",
		},
		{
			exprStr:   "a LIKE '%'",
			resultStr: "[[-inf +inf]]",
		},
		{
			exprStr:   `a LIKE '\%a'`,
			resultStr: `[[%a %b)]`,
		},
		{
			exprStr:   `a LIKE "\\"`,
			resultStr: `[[\ ])]`,
		},
		{
			exprStr:   `a LIKE "\\\\a%"`,
			resultStr: `[[\a \b)]`,
		},
		{
			exprStr:   `a > 0 AND a < 1`,
			resultStr: `[(0 1)]`,
		},
		{
			exprStr:   `a > 1 AND a < 0`,
			resultStr: `[]`,
		},
		{
			exprStr:   `a > 1 OR a < 0`,
			resultStr: `[[-inf 0) (1 +inf]]`,
		},
		{
			exprStr:   `(a > 1 AND a < 2) OR (a > 3 AND a < 4)`,
			resultStr: `[(1 2) (3 4)]`,
		},
		{
			exprStr:   `(a < 0 OR a > 3) AND (a < 1 OR a > 4)`,
			resultStr: `[[-inf 0) (4 +inf]]`,
		},
	}

	for _, ca := range cases {
		sql := "select 1 from dual where " + ca.exprStr
		lexer := parser.NewLexer(sql)

		rc := parser.YYParse(lexer)
		c.Assert(rc, Equals, 0, Commentf("error %v, for expr %", lexer.Errors(), ca.exprStr))
		stmt := lexer.Stmts()[0].(*ast.SelectStmt)
		result := rb.build(stmt.Where)
		c.Assert(rb.err, IsNil)
		got := fmt.Sprintf("%v", result)
		c.Assert(got, Equals, ca.resultStr, Commentf("differen for expr %s", ca.exprStr))
	}
}

func (s *testPlanSuite) TestBuilder(c *C) {
	log.SetLevel(log.LOG_LEVEL_ERROR)
	cases := []struct {
		sqlStr  string
		planStr string
	}{
		{
			sqlStr:  "select 1",
			planStr: "Fields",
		},
		{
			sqlStr:  "select a from t",
			planStr: "Table(t)->Fields",
		},
		{
			sqlStr:  "select a from t where a = 1",
			planStr: "Table(t)->Filter->Fields",
		},
		{
			sqlStr:  "select a from t where a = 1 order by a",
			planStr: "Table(t)->Filter->Fields->Sort",
		},
		{
			sqlStr:  "select a from t where a = 1 order by a limit 1",
			planStr: "Table(t)->Filter->Fields->Sort->Limit",
		},
		{
			sqlStr:  "select a from t where a = 1 limit 1",
			planStr: "Table(t)->Filter->Fields->Limit",
		},
		{
			sqlStr:  "select a from t where a = 1 limit 1 for update",
			planStr: "Table(t)->Filter->Lock->Fields->Limit",
		},
	}
	for _, ca := range cases {
		lexer := parser.NewLexer(ca.sqlStr)
		rc := parser.YYParse(lexer)
		c.Assert(rc, Equals, 0, Commentf("error %v for expr %s", lexer.Errors(), ca.sqlStr))
		stmt := lexer.Stmts()[0].(*ast.SelectStmt)
		mockResolve(stmt)
		p, err := BuildPlan(stmt)
		c.Assert(err, IsNil)
		explainStr, err := Explain(p)
		c.Assert(err, IsNil)
		c.Assert(ca.planStr, Equals, explainStr)
	}
}

func (s *testPlanSuite) TestBestPlan(c *C) {
	log.SetLevel(log.LOG_LEVEL_ERROR)
	cases := []struct {
		sql  string
		best string
	}{
		{
			sql:  "select * from t",
			best: "Table(t)->Fields",
		},
		{
			sql:  "select * from t order by a",
			best: "Index(t.a)->Fields",
		},
		{
			sql:  "select * from t where b = 1 order by a",
			best: "Index(t.b)->Filter->Fields->Sort",
		},
		{
			sql:  "select * from t where (a between 1 and 2) and (b = 3)",
			best: "Index(t.b)->Filter->Fields",
		},
		{
			sql:  "select * from t where a > 0 order by b limit 100",
			best: "Index(t.b)->Filter->Fields->Limit",
		},
		{
			sql:  "select * from t where d = 0",
			best: "Table(t)->Filter->Fields",
		},
		{
			sql:  "select * from t where c = 0 and d = 0",
			best: "Index(t.c_d)->Filter->Fields",
		},
		{
			sql:  "select * from t where a like 'abc%'",
			best: "Index(t.a)->Filter->Fields",
		},
		{
			sql:  "select * from t where d",
			best: "Table(t)->Filter->Fields",
		},
		{
			sql:  "select * from t where a is null",
			best: "Index(t.a)->Filter->Fields",
		},
	}
	for _, ca := range cases {
		lexer := parser.NewLexer(ca.sql)
		rc := parser.YYParse(lexer)
		c.Assert(rc, Equals, 0, Commentf("error %v for sql %s", lexer.Errors(), ca.sql))
		stmt := lexer.Stmts()[0].(*ast.SelectStmt)
		mockResolve(stmt)
		p, err := BuildPlan(stmt)
		c.Assert(err, IsNil)
		bestCost := EstimateCost(p)
		bestPlan := p
		alts, err := Alternatives(p)
		c.Assert(err, IsNil)
		for _, alt := range alts {
			cost := EstimateCost(alt)
			if cost < bestCost {
				bestCost = cost
				bestPlan = alt
			}
		}
		explainStr, err := Explain(bestPlan)
		c.Assert(err, IsNil)
		c.Assert(explainStr, Equals, ca.best, Commentf("for %s cost %v", ca.sql, bestCost))
	}
}

func mockResolve(node ast.Node) {
	indices := []*model.IndexInfo{
		{
			Name: model.NewCIStr("a"),
			Columns: []*model.IndexColumn{
				{
					Name: model.NewCIStr("a"),
				},
			},
		},
		{
			Name: model.NewCIStr("b"),
			Columns: []*model.IndexColumn{
				{
					Name: model.NewCIStr("b"),
				},
			},
		},
		{
			Name: model.NewCIStr("c_d"),
			Columns: []*model.IndexColumn{
				{
					Name: model.NewCIStr("c"),
				},
				{
					Name: model.NewCIStr("d"),
				},
			},
		},
	}
	table := &model.TableInfo{
		Indices: indices,
		Name:    model.NewCIStr("t"),
	}
	resolver := mockResolver{table: table}
	node.Accept(&resolver)
}

type mockResolver struct {
	table *model.TableInfo
}

func (b *mockResolver) Enter(in ast.Node) (ast.Node, bool) {
	return in, false
}

func (b *mockResolver) Leave(in ast.Node) (ast.Node, bool) {
	switch x := in.(type) {
	case *ast.ColumnNameExpr:
		x.Refer = &ast.ResultField{
			Column: &model.ColumnInfo{
				Name: x.Name.Name,
			},
			Table: b.table,
		}
	case *ast.TableName:
		x.TableInfo = b.table
	}
	return in, true
}
