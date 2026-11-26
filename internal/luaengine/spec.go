package luaengine

import (
	"fmt"
	"strings"
	"time"

	"github.com/yuin/gopher-lua"

	"github.com/ZaphireLiu/ntfy-notifier/internal/model"
)

func specFromTable(t *lua.LTable) *model.Spec {
	s := &model.Spec{
		Title:    luaString(t, "title"),
		Body:     luaString(t, "body"),
		Priority: int(luaNumber(t, "priority")),
		Sound:    strings.ToLower(luaString(t, "sound")),
		Duration: strings.ToLower(luaString(t, "duration")),
		Icon:     luaString(t, "icon"),
		Click:    luaString(t, "click"),
	}
	if a := luaTable(t, "on_click"); a != nil {
		if act, err := actionFromTable(a); err == nil {
			s.OnClick = act
		}
	}
	if btns := luaTable(t, "buttons"); btns != nil {
		btns.ForEach(func(_, v lua.LValue) {
			bt, ok := v.(*lua.LTable)
			if !ok {
				return
			}
			label := luaString(bt, "label")
			act, err := actionFromTable(bt)
			if err != nil || label == "" {
				return
			}
			s.Buttons = append(s.Buttons, model.Button{Label: label, Action: *act})
		})
	}
	return s
}

// actionFromTable accepts both the shorthand form
//
//	{ copy = "123", sensitive = true }
//
// and the explicit form with a type field.
func actionFromTable(t *lua.LTable) (*model.Action, error) {
	typ := strings.ToLower(luaString(t, "type"))
	copyText := luaString(t, "copy")
	openURL := luaString(t, "open")

	switch {
	case typ == "copy" || copyText != "":
		text := copyText
		if text == "" {
			text = luaString(t, "text")
		}
		if text == "" {
			return nil, fmt.Errorf("copy action requires text")
		}
		return &model.Action{
			Type:      model.ActionCopy,
			Text:      text,
			Sensitive: luaBool(t, "sensitive"),
		}, nil

	case typ == "open" || openURL != "":
		url := openURL
		if url == "" {
			url = luaString(t, "url")
		}
		if url == "" {
			return nil, fmt.Errorf("open action requires a url")
		}
		return &model.Action{Type: model.ActionOpen, URL: url}, nil

	case typ == "http" || luaTable(t, "http") != nil:
		sub := luaTable(t, "http")
		if sub == nil {
			sub = t
		}
		url := luaString(sub, "url")
		if url == "" {
			return nil, fmt.Errorf("http action requires a url")
		}
		method := strings.ToUpper(luaString(sub, "method"))
		if method == "" {
			method = "POST"
		}
		headers := map[string]string{}
		if h := luaTable(sub, "headers"); h != nil {
			h.ForEach(func(k, v lua.LValue) {
				headers[k.String()] = v.String()
			})
		}
		a := &model.Action{
			Type:    model.ActionHTTP,
			URL:     url,
			Method:  method,
			Headers: headers,
			Body:    luaString(sub, "body"),
		}
		if secs := luaNumber(sub, "timeout"); secs > 0 {
			a.Timeout = time.Duration(secs * float64(time.Second))
		}
		return a, nil

	case typ == "run" || luaTable(t, "run") != nil:
		sub := luaTable(t, "run")
		if sub == nil {
			sub = t
		}
		cmd := luaString(sub, "cmd")
		if cmd == "" {
			return nil, fmt.Errorf("run action requires cmd")
		}
		a := &model.Action{Type: model.ActionRun, Cmd: cmd}
		if args := luaTable(sub, "args"); args != nil {
			args.ForEach(func(_, v lua.LValue) {
				a.Args = append(a.Args, v.String())
			})
		}
		return a, nil
	}
	return nil, fmt.Errorf("unknown action")
}
