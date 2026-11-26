package luaengine

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/yuin/gopher-lua"

	"github.com/ZaphireLiu/ntfy-notifier/internal/model"
)

func messageToTable(L *lua.LState, m model.Message) *lua.LTable {
	t := L.NewTable()
	t.RawSetString("id", lua.LString(m.ID))
	t.RawSetString("time", lua.LNumber(m.Time))
	t.RawSetString("topic", lua.LString(m.Topic))
	t.RawSetString("title", lua.LString(m.Title))
	t.RawSetString("message", lua.LString(m.Message))
	t.RawSetString("priority", lua.LNumber(m.Priority))
	t.RawSetString("click", lua.LString(m.Click))
	t.RawSetString("icon", lua.LString(m.Icon))
	t.RawSetString("sender", lua.LString(m.Sender))
	t.RawSetString("raw", lua.LString(m.Raw))

	tags := L.NewTable()
	for i, tag := range m.Tags {
		tags.RawSetInt(i+1, lua.LString(tag))
	}
	t.RawSetString("tags", tags)

	actions := L.NewTable()
	for i, a := range m.Actions {
		at := L.NewTable()
		at.RawSetString("id", lua.LString(a.ID))
		at.RawSetString("action", lua.LString(a.Action))
		at.RawSetString("label", lua.LString(a.Label))
		at.RawSetString("url", lua.LString(a.URL))
		at.RawSetString("method", lua.LString(a.Method))
		at.RawSetString("body", lua.LString(a.Body))
		at.RawSetString("clear", lua.LBool(a.Clear))
		headers := L.NewTable()
		for k, v := range a.Headers {
			headers.RawSetString(k, lua.LString(v))
		}
		at.RawSetString("headers", headers)
		actions.RawSetInt(i+1, at)
	}
	t.RawSetString("actions", actions)

	if m.Attachment != nil {
		att := L.NewTable()
		att.RawSetString("name", lua.LString(m.Attachment.Name))
		att.RawSetString("url", lua.LString(m.Attachment.URL))
		att.RawSetString("type", lua.LString(m.Attachment.Type))
		att.RawSetString("size", lua.LNumber(m.Attachment.Size))
		att.RawSetString("expires", lua.LNumber(m.Attachment.Expires))
		t.RawSetString("attachment", att)
	}
	return t
}

// anyToLValue converts JSON-ish Go values into Lua values.
func anyToLValue(L *lua.LState, v any) lua.LValue {
	switch x := v.(type) {
	case nil:
		return lua.LNil
	case bool:
		return lua.LBool(x)
	case string:
		return lua.LString(x)
	case float64:
		return lua.LNumber(x)
	case float32:
		return lua.LNumber(x)
	case int:
		return lua.LNumber(x)
	case int64:
		return lua.LNumber(x)
	case json.Number:
		if f, err := x.Float64(); err == nil {
			return lua.LNumber(f)
		}
		return lua.LString(x.String())
	case []any:
		t := L.NewTable()
		for i, item := range x {
			t.RawSetInt(i+1, anyToLValue(L, item))
		}
		return t
	case []string:
		t := L.NewTable()
		for i, item := range x {
			t.RawSetInt(i+1, lua.LString(item))
		}
		return t
	case map[string]any:
		t := L.NewTable()
		for k, item := range x {
			t.RawSetString(k, anyToLValue(L, item))
		}
		return t
	case map[string]string:
		t := L.NewTable()
		for k, item := range x {
			t.RawSetString(k, lua.LString(item))
		}
		return t
	default:
		return lua.LString(fmt.Sprint(v))
	}
}

// luaToAny converts a Lua value into a JSON-serialisable Go value.
func luaToAny(v lua.LValue) any {
	switch x := v.(type) {
	case *lua.LNilType:
		return nil
	case lua.LBool:
		return bool(x)
	case lua.LNumber:
		return float64(x)
	case lua.LString:
		return string(x)
	case *lua.LTable:
		return luaTableToAny(x)
	default:
		return v.String()
	}
}

func luaTableToAny(t *lua.LTable) any {
	length := t.Len()
	count := 0
	isArray := true
	t.ForEach(func(k, _ lua.LValue) {
		count++
		n, ok := k.(lua.LNumber)
		if !ok || float64(n) != float64(count) {
			isArray = false
		}
	})
	if count == 0 {
		return map[string]any{}
	}
	if isArray && count == length {
		out := make([]any, 0, length)
		for i := 1; i <= length; i++ {
			out = append(out, luaToAny(t.RawGetInt(i)))
		}
		return out
	}
	out := make(map[string]any, count)
	t.ForEach(func(k, v lua.LValue) {
		out[k.String()] = luaToAny(v)
	})
	return out
}

// luaTableToMap is used for ctx.kv, which is always a string-keyed table.
func luaTableToMap(t *lua.LTable) map[string]any {
	if t == nil {
		return map[string]any{}
	}
	out := make(map[string]any)
	t.ForEach(func(k, v lua.LValue) {
		out[k.String()] = luaToAny(v)
	})
	return out
}

func luaString(t *lua.LTable, key string) string {
	v := t.RawGetString(key)
	switch v.Type() {
	case lua.LTNil:
		return ""
	case lua.LTString, lua.LTNumber:
		return v.String()
	default:
		return ""
	}
}

func luaNumber(t *lua.LTable, key string) float64 {
	v := t.RawGetString(key)
	if n, ok := v.(lua.LNumber); ok {
		return float64(n)
	}
	return 0
}

func luaBool(t *lua.LTable, key string) bool {
	v := t.RawGetString(key)
	if b, ok := v.(lua.LBool); ok {
		return bool(b)
	}
	return false
}

func luaTable(t *lua.LTable, key string) *lua.LTable {
	v := t.RawGetString(key)
	if tt, ok := v.(*lua.LTable); ok {
		return tt
	}
	return nil
}

func unixOrNow(ts int64) time.Time {
	if ts <= 0 {
		return time.Now()
	}
	return time.Unix(ts, 0)
}
