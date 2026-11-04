local journal = require("aeorbit.journal")
local files = {}
local broken = false
local fs = {
  read = function(path) return files[path] end,
  write = function(path, data) if broken then files[path]="partial"; error("disk full") end; files[path]=data; return true end
}
local j = journal.new(fs,"journal",8192)
j:save({attempts={a={state="intent"}},completed={}})
j:save({attempts={a={state="running"}},completed={}})
local recovered = journal.new(fs,"journal",8192)
assert(recovered.data.attempts.a.state=="running")
broken=true
assert(not pcall(function() recovered:save({attempts={},completed={}}) end))
assert(journal.new(fs,"journal",8192).data.attempts.a.state=="running", "torn write lost the last valid record")
files["bad.0"]="partial"; files["bad.1"]="also partial"
assert(not pcall(function() journal.new(fs,"bad",8192) end), "corruption silently reset execution history")
