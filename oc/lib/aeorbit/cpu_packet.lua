local json=require("aeorbit.json")
local M={}
-- Reserve space for every basic record before optional output details. Repeated
-- names stay separate; truncation means CPU records were omitted, not outputs.
function M.pack(body,cpus,truncated,count_limit,byte_limit)
  body.cpus=json.array({});body.cpus_truncated=truncated==true
  for _,cpu in ipairs(cpus) do
    local basic={id=cpu.id,name=cpu.name,busy=cpu.busy,storage=cpu.storage,coprocessors=cpu.coprocessors}
    body.cpus[#body.cpus+1]=basic
    if #body.cpus>count_limit or not pcall(json.encode,body,byte_limit) then
      body.cpus[#body.cpus]=nil;body.cpus_truncated=true;break
    end
  end
  for index,basic in ipairs(body.cpus) do
    local detail=cpus[index]
    basic.output=detail.output;basic.output_unavailable=detail.output_unavailable
    if not pcall(json.encode,body,byte_limit) then
      basic.output=nil;basic.output_unavailable="packet budget"
      if not pcall(json.encode,body,byte_limit) then basic.output_unavailable=nil end
    end
  end
end
return M
