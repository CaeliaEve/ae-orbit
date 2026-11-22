local monitor=require('aeorbit.monitor')
local prefix='kekztech.infodata.lapotronic_super_capacitor.'
local function line(key,value)return prefix..key..'\\\\'..value end
local lines={line('eu_in','2,621,440'),line('eu_out','155,790,541'),line('passive_loss','70,174'),line('avg_eu_in.sec','2,097,152\\\\5'),line('avg_eu_out.sec','107,431,812\\\\5')}
local f=assert(monitor.parseFlow(lines))
assert(f.input_eut=='2621440' and f.output_eut=='155790541' and f.loss_eut=='70174' and f.avg_input_eut=='2097152' and f.avg_output_eut=='107431812' and f.window_seconds==5)
local a,b=monitor.flow({getSensorInformation=function()return lines end})
assert(a.avg_output_eut==f.avg_output_eut and b==nil)
assert(select(2,monitor.flow({}))=='unsupported')
assert(select(2,monitor.flow({getSensorInformation=function()error('failed')end}))=='read_failed')
for _,bad in ipairs({'1e10','1.5','-1','12,34','01',''})do
 local copy={table.unpack(lines)};copy[1]=line('eu_in',bad)
 assert(select(2,monitor.parseFlow(copy))=='invalid_format',bad)
end
local copy={table.unpack(lines)};copy[4]=line('avg_eu_in.sec','200\\\\60');assert(not monitor.parseFlow(copy))
copy={table.unpack(lines)};copy[#copy+1]=lines[1];assert(not monitor.parseFlow(copy))
assert(not monitor.parseFlow({'EU IN: 2.6M EU/t'}),'localized or rounded text must not be guessed')
copy={table.unpack(lines)};copy[1]=line('eu_in',string.rep('9',128));assert(monitor.parseFlow(copy).input_eut==string.rep('9',128))
local ok=pcall(function()monitor.flow({getSensorInformation=function()error('interrupted',0)end})end);assert(not ok)
