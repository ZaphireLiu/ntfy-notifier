package luaengine

import (
	"context"
	"encoding/json"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/yuin/gopher-lua"
)

// newSandbox returns an LState with a deliberately small standard library.
func newSandbox(topic string, log *slog.Logger) *lua.LState {
	L := lua.NewState(lua.Options{SkipOpenLibs: true})
	lua.OpenBase(L)
	lua.OpenTable(L)
	lua.OpenString(L)
	lua.OpenMath(L)
	lua.OpenOs(L)

	for _, name := range []string{"dofile", "loadfile", "load", "loadstring", "require", "module", "getfenv", "setfenv"} {
		L.SetGlobal(name, lua.LNil)
	}
	if osTable, ok := L.GetGlobal("os").(*lua.LTable); ok {
		for _, name := range []string{"execute", "exit", "remove", "rename", "setenv", "getenv", "setlocale", "tmpname"} {
			osTable.RawSetString(name, lua.LNil)
		}
	}

	L.SetGlobal("print", L.NewFunction(func(L *lua.LState) int {
		parts := make([]string, 0, L.GetTop())
		for i := 1; i <= L.GetTop(); i++ {
			parts = append(parts, L.Get(i).String())
		}
		log.Info("lua print", "topic", topic, "text", strings.Join(parts, " "))
		return 0
	}))

	ntfyTable := L.NewTable()
	L.SetField(ntfyTable, "version", lua.LNumber(1))
	L.SetField(ntfyTable, "log", L.NewFunction(func(L *lua.LState) int {
		level := strings.ToLower(L.CheckString(1))
		msg := L.CheckString(2)
		var lvl slog.Level
		switch level {
		case "debug":
			lvl = slog.LevelDebug
		case "warn", "warning":
			lvl = slog.LevelWarn
		case "error":
			lvl = slog.LevelError
		default:
			lvl = slog.LevelInfo
		}
		ctx := L.Context()
		if ctx == nil {
			ctx = context.Background()
		}
		log.Log(ctx, lvl, msg, "topic", topic)
		return 0
	}))
	L.SetField(ntfyTable, "now", L.NewFunction(func(L *lua.LState) int {
		L.Push(lua.LNumber(time.Now().Unix()))
		return 1
	}))
	L.SetField(ntfyTable, "match", L.NewFunction(func(L *lua.LState) int {
		s := L.CheckString(1)
		pattern := L.CheckString(2)
		re, err := regexp.Compile(pattern)
		if err != nil {
			log.Warn("lua regexp error", "topic", topic, "pattern", pattern, "err", err)
			L.Push(lua.LFalse)
			return 1
		}
		L.Push(lua.LBool(re.MatchString(s)))
		return 1
	}))
	L.SetField(ntfyTable, "gsub", L.NewFunction(func(L *lua.LState) int {
		s := L.CheckString(1)
		pattern := L.CheckString(2)
		repl := L.CheckString(3)
		re, err := regexp.Compile(pattern)
		if err != nil {
			log.Warn("lua regexp error", "topic", topic, "pattern", pattern, "err", err)
			L.Push(lua.LString(s))
			return 1
		}
		L.Push(lua.LString(re.ReplaceAllString(s, repl)))
		return 1
	}))
	L.SetField(ntfyTable, "format_time", L.NewFunction(func(L *lua.LState) int {
		ts := L.CheckInt64(1)
		layout := L.OptString(2, "2006-01-02 15:04:05")
		L.Push(lua.LString(time.Unix(ts, 0).Format(layout)))
		return 1
	}))

	jsonTable := L.NewTable()
	L.SetField(jsonTable, "decode", L.NewFunction(func(L *lua.LState) int {
		s := L.CheckString(1)
		var v any
		if err := json.Unmarshal([]byte(s), &v); err != nil {
			L.Push(lua.LNil)
			return 1
		}
		L.Push(anyToLValue(L, v))
		return 1
	}))
	L.SetField(jsonTable, "encode", L.NewFunction(func(L *lua.LState) int {
		v := luaToAny(L.Get(1))
		b, err := json.Marshal(v)
		if err != nil {
			L.Push(lua.LNil)
			return 1
		}
		L.Push(lua.LString(b))
		return 1
	}))
	L.SetField(ntfyTable, "json", jsonTable)
	L.SetGlobal("ntfy", ntfyTable)

	return L
}
