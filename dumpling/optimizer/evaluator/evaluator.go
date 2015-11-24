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

package evaluator

import (
	"strings"

	"github.com/juju/errors"
	"github.com/ngaut/log"
	"github.com/pingcap/tidb/ast"
	"github.com/pingcap/tidb/context"
	"github.com/pingcap/tidb/expression/builtin"
	"github.com/pingcap/tidb/mysql"
	"github.com/pingcap/tidb/parser/opcode"
	"github.com/pingcap/tidb/sessionctx/variable"
	"github.com/pingcap/tidb/util/charset"
	"github.com/pingcap/tidb/util/types"
	"golang.org/x/text/transform"
)

// Eval evaluates an expression to a value.
func Eval(ctx context.Context, expr ast.ExprNode) (interface{}, error) {
	e := &Evaluator{ctx: ctx}
	expr.Accept(e)
	if e.err != nil {
		return nil, errors.Trace(e.err)
	}
	return expr.GetValue(), nil
}

// EvalBool evalueates an expression to a boolean value.
func EvalBool(ctx context.Context, expr ast.ExprNode) (bool, error) {
	val, err := Eval(ctx, expr)
	if err != nil {
		return false, errors.Trace(err)
	}
	if val == nil {
		return false, nil
	}
	i, err := types.ToBool(val)
	if err != nil {
		return false, errors.Trace(err)
	}
	return i != 0, nil
}

// Evaluator is a ast Visitor that evaluates an expression.
type Evaluator struct {
	ctx context.Context
	err error
}

// Enter implements ast.Visitor interface.
func (e *Evaluator) Enter(in ast.Node) (out ast.Node, skipChildren bool) {
	return in, false
}

// Leave implements ast.Visitor interface.
func (e *Evaluator) Leave(in ast.Node) (out ast.Node, ok bool) {
	switch v := in.(type) {
	case *ast.ValueExpr:
		ok = true
	case *ast.BetweenExpr:
		ok = e.between(v)
	case *ast.BinaryOperationExpr:
		ok = e.binaryOperation(v)
	case *ast.CaseExpr:
		ok = e.caseExpr(v)
	case *ast.WhenClause:
		ok = true
	case *ast.SubqueryExpr:
		ok = e.subquery(v)
	case *ast.CompareSubqueryExpr:
		ok = e.compareSubquery(v)
	case *ast.ColumnName:
		ok = true
	case *ast.ColumnNameExpr:
		ok = e.columnName(v)
	case *ast.DefaultExpr:
		ok = e.defaultExpr(v)
	case *ast.ExistsSubqueryExpr:
		ok = e.existsSubquery(v)
	case *ast.PatternInExpr:
		ok = e.patternIn(v)
	case *ast.IsNullExpr:
		ok = e.isNull(v)
	case *ast.IsTruthExpr:
		ok = e.isTruth(v)
	case *ast.PatternLikeExpr:
		ok = e.patternLike(v)
	case *ast.ParamMarkerExpr:
		ok = e.paramMarker(v)
	case *ast.ParenthesesExpr:
		ok = e.parentheses(v)
	case *ast.PositionExpr:
		ok = e.position(v)
	case *ast.PatternRegexpExpr:
		ok = e.patternRegexp(v)
	case *ast.RowExpr:
		ok = e.row(v)
	case *ast.UnaryOperationExpr:
		ok = e.unaryOperation(v)
	case *ast.ValuesExpr:
		ok = e.values(v)
	case *ast.VariableExpr:
		ok = e.variable(v)
	case *ast.FuncCallExpr:
		ok = e.funcCall(v)
	case *ast.FuncExtractExpr:
		ok = e.funcExtract(v)
	case *ast.FuncConvertExpr:
		ok = e.funcConvert(v)
	case *ast.FuncCastExpr:
		ok = e.funcCast(v)
	case *ast.FuncSubstringExpr:
		ok = e.funcSubstring(v)
	case *ast.FuncSubstringIndexExpr:
		ok = e.funcSubstringIndex(v)
	case *ast.FuncLocateExpr:
		ok = e.funcLocate(v)
	case *ast.FuncTrimExpr:
		ok = e.funcTrim(v)
	case *ast.AggregateFuncExpr:
		ok = e.aggregateFunc(v)
	}
	out = in
	return
}

func (e *Evaluator) between(v *ast.BetweenExpr) bool {
	var l, r ast.ExprNode
	op := opcode.AndAnd

	if v.Not {
		// v < lv || v > rv
		op = opcode.OrOr
		l = &ast.BinaryOperationExpr{Op: opcode.LT, L: v.Expr, R: v.Left}
		r = &ast.BinaryOperationExpr{Op: opcode.GT, L: v.Expr, R: v.Right}
	} else {
		// v >= lv && v <= rv
		l = &ast.BinaryOperationExpr{Op: opcode.GE, L: v.Expr, R: v.Left}
		r = &ast.BinaryOperationExpr{Op: opcode.LE, L: v.Expr, R: v.Right}
	}

	ret := &ast.BinaryOperationExpr{Op: op, L: l, R: r}
	ret.Accept(e)
	if e.err != nil {
		return false
	}
	v.SetValue(ret.GetValue())
	return true
}

func (e *Evaluator) caseExpr(v *ast.CaseExpr) bool {
	var target interface{} = true
	if v.Value != nil {
		target = v.Value.GetValue()
	}
	if target != nil {
		for _, val := range v.WhenClauses {
			cmp, err := types.Compare(target, val.Expr.GetValue())
			if err != nil {
				e.err = err
				return false
			}
			if cmp == 0 {
				v.SetValue(val.Result.GetValue())
				return true
			}
		}
	}
	if v.ElseClause != nil {
		v.SetValue(v.ElseClause.GetValue())
	}
	return true
}

func (e *Evaluator) subquery(v *ast.SubqueryExpr) bool {
	return true
}

func (e *Evaluator) compareSubquery(v *ast.CompareSubqueryExpr) bool {
	return true
}

func (e *Evaluator) columnName(v *ast.ColumnNameExpr) bool {
	v.SetValue(v.Refer.Expr.GetValue())
	return true
}

func (e *Evaluator) defaultExpr(v *ast.DefaultExpr) bool {
	return true
}

func (e *Evaluator) existsSubquery(v *ast.ExistsSubqueryExpr) bool {
	return true
}

func (e *Evaluator) checkInList(not bool, in interface{}, list []interface{}) (interface{}, error) {
	hasNull := false
	for _, v := range list {
		if types.IsNil(v) {
			hasNull = true
			continue
		}

		r, err := types.Compare(in, v)
		if err != nil {
			return nil, err
		}

		if r == 0 {
			return !not, nil
		}
	}

	if hasNull {
		// if no matched but we got null in In, return null
		// e.g 1 in (null, 2, 3) returns null
		return nil, nil
	}

	return not, nil
}

func (e *Evaluator) patternIn(n *ast.PatternInExpr) bool {
	lhs := n.Expr.GetValue()
	if types.IsNil(lhs) {
		n.SetValue(nil)
		return true
	}
	hasNull := false
	for _, v := range n.List {
		if types.IsNil(v.GetValue()) {
			hasNull = true
			continue
		}
		r, err := types.Compare(n.Expr.GetValue(), v.GetValue())
		if err != nil {
			e.err = err
			return false
		}
		if r == 0 {
			n.SetValue(!n.Not)
			return true
		}
	}
	if hasNull {
		// if no matched but we got null in In, return null
		// e.g 1 in (null, 2, 3) returns null
		n.SetValue(nil)
		return true
	}
	n.SetValue(n.Not)
	return true
}

func (e *Evaluator) isNull(v *ast.IsNullExpr) bool {
	var boolVal bool
	if types.IsNil(v.Expr.GetValue()) {
		boolVal = true
	}
	if v.Not {
		boolVal = !boolVal
	}
	v.SetValue(boolVal)
	return true
}

func (e *Evaluator) isTruth(v *ast.IsTruthExpr) bool {
	var boolVal bool
	val := v.Expr.GetValue()
	if !types.IsNil(val) {
		ival, err := types.ToBool(val)
		if err != nil {
			e.err = err
			return false
		}
		if ival == v.True {
			boolVal = true
		}
	}
	if v.Not {
		boolVal = !boolVal
	}
	v.SetValue(boolVal)
	return true
}

func (e *Evaluator) paramMarker(v *ast.ParamMarkerExpr) bool {
	return true
}

func (e *Evaluator) parentheses(v *ast.ParenthesesExpr) bool {
	v.SetValue(v.Expr.GetValue())
	return true
}

func (e *Evaluator) position(v *ast.PositionExpr) bool {
	v.SetValue(v.Refer.Expr.GetValue())
	return true
}

func (e *Evaluator) row(v *ast.RowExpr) bool {
	var row []interface{}
	for _, val := range v.Values {
		row = append(row, val.GetValue())
	}
	v.SetValue(row)
	return true
}

func (e *Evaluator) unaryOperation(u *ast.UnaryOperationExpr) bool {
	defer func() {
		if er := recover(); er != nil {
			e.err = errors.Errorf("%v", er)
		}
	}()
	a := u.V.GetValue()
	a = types.RawData(a)
	if a == nil {
		return true
	}
	switch op := u.Op; op {
	case opcode.Not:
		n, err := types.ToBool(a)
		if err != nil {
			e.err = err
		} else if n == 0 {
			u.SetValue(int64(1))
		} else {
			u.SetValue(int64(0))
		}
	case opcode.BitNeg:
		// for bit operation, we will use int64 first, then return uint64
		n, err := types.ToInt64(a)
		if err != nil {
			e.err = err
			return false
		}
		u.SetValue(uint64(^n))
	case opcode.Plus:
		switch x := a.(type) {
		case nil:
		case bool:
			if x {
				u.SetValue(int64(1))
			} else {
				u.SetValue(int64(0))
			}
		case float32:
			u.SetValue(+x)
		case float64:
			u.SetValue(+x)
		case int:
			u.SetValue(+x)
		case int8:
			u.SetValue(+x)
		case int16:
			u.SetValue(+x)
		case int32:
			u.SetValue(+x)
		case int64:
			u.SetValue(+x)
		case uint:
			u.SetValue(+x)
		case uint8:
			u.SetValue(+x)
		case uint16:
			u.SetValue(+x)
		case uint32:
			u.SetValue(+x)
		case uint64:
			u.SetValue(+x)
		case mysql.Duration:
			u.SetValue(x)
		case mysql.Time:
			u.SetValue(x)
		case string:
			u.SetValue(x)
		case mysql.Decimal:
			u.SetValue(x)
		case []byte:
			u.SetValue(x)
		case mysql.Hex:
			u.SetValue(x)
		case mysql.Bit:
			u.SetValue(x)
		case mysql.Enum:
			u.SetValue(x)
		case mysql.Set:
			u.SetValue(x)
		default:
			e.err = errors.Errorf("invalid operation")
			return false
		}
	case opcode.Minus:
		switch x := a.(type) {
		case nil:
		case bool:
			if x {
				u.SetValue(int64(-1))
			} else {
				u.SetValue(int64(0))
			}
		case float32:
			u.SetValue(-x)
		case float64:
			u.SetValue(-x)
		case int:
			u.SetValue(-x)
		case int8:
			u.SetValue(-x)
		case int16:
			u.SetValue(-x)
		case int32:
			u.SetValue(-x)
		case int64:
			u.SetValue(-x)
		case uint:
			u.SetValue(-int64(x))
		case uint8:
			u.SetValue(-int64(x))
		case uint16:
			u.SetValue(-int64(x))
		case uint32:
			u.SetValue(-int64(x))
		case uint64:
			// TODO: check overflow and do more test for unsigned type
			u.SetValue(-int64(x))
		case mysql.Duration:
			u.SetValue(mysql.ZeroDecimal.Sub(x.ToNumber()))
		case mysql.Time:
			u.SetValue(mysql.ZeroDecimal.Sub(x.ToNumber()))
		case string:
			f, err := types.StrToFloat(x)
			e.err = err
			u.SetValue(-f)
		case mysql.Decimal:
			f, _ := x.Float64()
			u.SetValue(mysql.NewDecimalFromFloat(-f))
		case []byte:
			f, err := types.StrToFloat(string(x))
			e.err = err
			u.SetValue(-f)
		case mysql.Hex:
			u.SetValue(-x.ToNumber())
		case mysql.Bit:
			u.SetValue(-x.ToNumber())
		case mysql.Enum:
			u.SetValue(-x.ToNumber())
		case mysql.Set:
			u.SetValue(-x.ToNumber())
		default:
			e.err = errors.Errorf("invalid operation")
		}
	default:
		panic("should never happen")
	}

	return true
}

func (e *Evaluator) values(v *ast.ValuesExpr) bool {
	return true
}

func (e *Evaluator) variable(v *ast.VariableExpr) bool {
	name := strings.ToLower(v.Name)
	sessionVars := variable.GetSessionVars(e.ctx)
	globalVars := variable.GetGlobalVarAccessor(e.ctx)
	if !v.IsSystem {
		// user vars
		if value, ok := sessionVars.Users[name]; ok {
			v.SetValue(value)
			return true
		}
		// select null user vars is permitted.
		v.SetValue(nil)
		return true
	}

	_, ok := variable.SysVars[name]
	if !ok {
		// select null sys vars is not permitted
		e.err = errors.Errorf("Unknown system variable '%s'", name)
		return false
	}

	if !v.IsGlobal {
		if value, ok := sessionVars.Systems[name]; ok {
			v.SetValue(value)
			return true
		}
	}
	value, err := globalVars.GetGlobalSysVar(e.ctx, name)
	if err != nil {
		e.err = errors.Trace(err)
		return false
	}
	v.SetValue(value)
	return true
}

func (e *Evaluator) funcCall(v *ast.FuncCallExpr) bool {
	f, ok := builtin.Funcs[v.FnName.L]
	if !ok {
		e.err = errors.Errorf("unknown function %s", v.FnName.O)
		return false
	}
	a := make([]interface{}, len(v.Args))
	for i, arg := range v.Args {
		a[i] = arg.GetValue()
	}
	var argMap map[interface{}]interface{}
	switch v.FnName.L {
	case "current_user", "database", "found_rows", "user":
		argMap = make(map[interface{}]interface{})
		argMap[builtin.ExprEvalArgCtx] = e.ctx
	}
	val, err := f.F(a, argMap)
	if err != nil {
		e.err = err
		return false
	}
	v.SetValue(val)
	return true
}

func (e *Evaluator) funcExtract(v *ast.FuncExtractExpr) bool {
	val := v.Date.GetValue()
	if val == nil {
		v.SetValue(nil)
		return true
	}

	f := types.NewFieldType(mysql.TypeDatetime)
	f.Decimal = mysql.MaxFsp
	var err error
	val, err = types.Convert(val, f)
	if err != nil {
		e.err = errors.Trace(err)
		return false
	}
	if val == nil {
		v.SetValue(nil)
		return true
	}

	t, ok := val.(mysql.Time)
	if !ok {
		e.err = errors.Errorf("need time type, but got %T", val)
		return false
	}
	n, err1 := mysql.ExtractTimeNum(v.Unit, t)
	if err1 != nil {
		e.err = errors.Trace(err1)
		return false
	}
	v.SetValue(n)
	return true
}

func (e *Evaluator) funcConvert(f *ast.FuncConvertExpr) bool {
	value := f.Expr.GetValue()

	// Casting nil to any type returns nil
	if value == nil {
		return true
	}
	str, ok := value.(string)
	if !ok {
		return true
	}
	if strings.ToLower(f.Charset) == "ascii" {
		f.SetValue(value)
		return true
	} else if strings.ToLower(f.Charset) == "utf8mb4" {
		f.SetValue(value)
		return true
	}

	encoding, _ := charset.Lookup(f.Charset)
	if encoding == nil {
		e.err = errors.Errorf("unknown encoding: %s", f.Charset)
		return false
	}

	target, _, err := transform.String(encoding.NewDecoder(), str)
	if err != nil {
		log.Errorf("Convert %s to %s with error: %v", str, f.Charset, err)
		e.err = errors.Trace(err)
		return false
	}
	f.SetValue(target)
	return true
}

func (e *Evaluator) funcCast(v *ast.FuncCastExpr) bool {
	value := v.Expr.GetValue()
	d := &types.DataItem{Type: v.Tp}
	// Casting nil to any type returns null
	if value == nil {
		d.Data = nil
		v.SetValue(d)
		return true
	}
	var err error
	d.Data, err = types.Cast(value, v.Tp)
	if err != nil {
		e.err = err
		return false
	}
	v.SetValue(d)
	return true
}

func (e *Evaluator) funcSubstring(v *ast.FuncSubstringExpr) bool {
	str, err := types.ToString(v.StrExpr.GetValue())
	if err != nil {
		e.err = errors.Errorf("Substring invalid args, need string but get %T", v.StrExpr.GetValue())
		return false
	}

	t := v.Pos.GetValue()
	p, ok := t.(int64)
	if !ok {
		e.err = errors.Errorf("Substring invalid pos args, need int but get %T", t)
		return false
	}
	pos := int(p)

	length := -1
	if v.Len != nil {
		t = v.Len.GetValue()
		p, ok = t.(int64)
		if !ok {
			e.err = errors.Errorf("Substring invalid pos args, need int but get %T", t)
			return false
		}
		length = int(p)
	}
	// The forms without a len argument return a substring from string str starting at position pos.
	// The forms with a len argument return a substring len characters long from string str, starting at position pos.
	// The forms that use FROM are standard SQL syntax. It is also possible to use a negative value for pos.
	// In this case, the beginning of the substring is pos characters from the end of the string, rather than the beginning.
	// A negative value may be used for pos in any of the forms of this function.
	if pos < 0 {
		pos = len(str) + pos
	} else {
		pos--
	}
	if pos > len(str) || pos <= 0 {
		pos = len(str)
	}
	end := len(str)
	if length != -1 {
		end = pos + length
	}
	if end > len(str) {
		end = len(str)
	}
	v.SetValue(str[pos:end])
	return true
}

func (e *Evaluator) funcSubstringIndex(v *ast.FuncSubstringIndexExpr) bool {
	fs := v.StrExpr.GetValue()
	str, err := types.ToString(fs)
	if err != nil {
		e.err = errors.Errorf("Substring_Index invalid args, need string but get %T", fs)
		return false
	}

	t := v.Delim.GetValue()
	delim, err := types.ToString(t)
	if err != nil {
		e.err = errors.Errorf("Substring_Index invalid delim, need string but get %T", t)
		return false
	}

	t = v.Count.GetValue()
	c, err := types.ToInt64(t)
	if err != nil {
		e.err = errors.Trace(err)
		return false
	}
	count := int(c)
	strs := strings.Split(str, delim)
	var (
		start = 0
		end   = len(strs)
	)
	if count > 0 {
		// If count is positive, everything to the left of the final delimiter (counting from the left) is returned.
		if count < end {
			end = count
		}
	} else {
		// If count is negative, everything to the right of the final delimiter (counting from the right) is returned.
		count = -count
		if count < end {
			start = end - count
		}
	}
	substrs := strs[start:end]
	v.SetValue(strings.Join(substrs, delim))
	return true
}

func (e *Evaluator) funcLocate(v *ast.FuncLocateExpr) bool {
	// eval str
	fs := v.Str.GetValue()
	if types.IsNil(fs) {
		v.SetValue(nil)
		return true
	}
	str, err := types.ToString(fs)
	if err != nil {
		e.err = err
		return false
	}
	// eval substr
	fs = v.SubStr.GetValue()
	if types.IsNil(fs) {
		v.SetValue(nil)
		return true
	}
	substr, err := types.ToString(fs)
	if err != nil {
		e.err = err
		return false
	}
	// eval pos
	pos := 0
	if v.Pos != nil {
		t := v.Pos.GetValue()
		p, err := types.ToInt64(t)
		if err != nil {
			e.err = errors.Trace(err)
			return false
		}
		pos = int(p)
	}
	// eval locate
	if pos < 0 || pos > len(str) {
		e.err = errors.Errorf("Locate invalid pos args: %d", pos)
		return false
	}
	str = str[pos:]
	i := strings.Index(str, substr)
	v.SetValue(i + 1 + pos)
	return true
}

const spaceChars = "\n\t\r "

func (e *Evaluator) funcTrim(v *ast.FuncTrimExpr) bool {
	// eval str
	fs := v.Str.GetValue()
	if types.IsNil(fs) {
		v.SetValue(nil)
		return true
	}
	str, err := types.ToString(fs)
	if err != nil {
		e.err = errors.Trace(err)
		return false
	}
	remstr := ""
	// eval remstr
	if v.RemStr != nil {
		fs = v.RemStr.GetValue()
		if types.IsNil(fs) {
			v.SetValue(nil)
			return true
		}
		remstr, err = types.ToString(fs)
		if err != nil {
			e.err = errors.Trace(err)
			return false
		}
	}
	// Do trim
	var result string
	if v.Direction == ast.TrimLeading {
		if len(remstr) > 0 {
			result = trimLeft(str, remstr)
		} else {
			result = strings.TrimLeft(str, spaceChars)
		}
	} else if v.Direction == ast.TrimTrailing {
		if len(remstr) > 0 {
			result = trimRight(str, remstr)
		} else {
			result = strings.TrimRight(str, spaceChars)
		}
	} else if len(remstr) > 0 {
		x := trimLeft(str, remstr)
		result = trimRight(x, remstr)
	} else {
		result = strings.Trim(str, spaceChars)
	}
	v.SetValue(result)
	return true
}

func trimLeft(str, remstr string) string {
	for {
		x := strings.TrimPrefix(str, remstr)
		if len(x) == len(str) {
			return x
		}
		str = x
	}
}

func trimRight(str, remstr string) string {
	for {
		x := strings.TrimSuffix(str, remstr)
		if len(x) == len(str) {
			return x
		}
		str = x
	}
}

func (e *Evaluator) aggregateFunc(v *ast.AggregateFuncExpr) bool {
	return true
}
