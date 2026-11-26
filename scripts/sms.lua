-- scripts/sms.lua
-- 期望的 ntfy 消息体：
-- {"vcode":"123456","name":"示例公司","sent_by":"10690000000000","timestamp":"2026-01-01 12:00:00"}

local function present(s)
  return s ~= nil and s ~= ""
end

function on_message(m)
  local ok, d = pcall(ntfy.json.decode, m.message)
  if not ok or type(d) ~= "table" or not present(d.vcode) then
    ntfy.log("warn", "sms 解析失败: " .. m.id)
    return { title = "短消息", body = m.message }
  end

  local parts = {}
  if present(d.name)    then table.insert(parts, d.name)    end
  if present(d.sent_by) then table.insert(parts, d.sent_by) end
  if #parts == 0        then parts[1] = "未知发送方"         end
  local first = table.concat(parts, " · ")

  return {
    title    = "验证码 " .. d.vcode,
    body     = first .. "\n" .. (d.timestamp or ""),
    priority = 4,
    sound    = "mail",
    on_click = { copy = d.vcode, sensitive = true },
    buttons  = {
      { label = "复制验证码", copy = d.vcode, sensitive = true },
    },
  }
end
