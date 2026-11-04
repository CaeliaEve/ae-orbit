local M = {}
function M.new(max_entries,max_bytes)
  assert(max_entries>=1 and max_bytes>=1,"cache: invalid budget")
  local cache={count=0,bytes=0,entries={},first=nil,last=nil}
  local function detach(e)
    if e.previous then e.previous.next=e.next else cache.first=e.next end
    if e.next then e.next.previous=e.previous else cache.last=e.previous end
  end
  local function front(e)
    e.previous=nil;e.next=cache.first
    if cache.first then cache.first.previous=e else cache.last=e end
    cache.first=e
  end
  function cache:get(key)
    local e=self.entries[key];if not e then return nil end
    detach(e);front(e);return e.value
  end
  function cache:remove(key)
    local e=self.entries[key];if not e then return end
    detach(e);self.entries[key]=nil;self.count=self.count-1;self.bytes=self.bytes-e.size
  end
  function cache:put(key,value,size)
    assert(size and size>=0,"cache: size required")
    if size>max_bytes then return false end
    self:remove(key)
    while self.count>=max_entries or self.bytes+size>max_bytes do self:remove(self.last.key) end
    local e={key=key,value=value,size=size};self.entries[key]=e;front(e)
    self.count=self.count+1;self.bytes=self.bytes+size;return true
  end
  function cache:pop()
    if not self.last then return nil end
    local e=self.last;self:remove(e.key);return e.key,e.value
  end
  return cache
end
return M
