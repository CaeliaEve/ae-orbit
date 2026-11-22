import test from 'node:test';
import assert from 'node:assert/strict';
import {lscAlertRuleHTML,lscAlertSettings} from '../src/lsc-alert-settings.mjs';
const original={alerts:true,low_percent:20,recover_percent:25,low_duration_seconds:30,interval_seconds:1,history_interval_seconds:10};
test('low-energy controls keep sampling and history unchanged',()=>{
 const settings=lscAlertSettings(original,{low_percent:'35',recover_percent:'40',low_duration_seconds:'0',alerts:true});
 assert.equal(settings.interval_seconds,1);assert.equal(settings.history_interval_seconds,10);assert.equal(original.low_percent,20);
 const html=lscAlertRuleHTML(settings,true);assert.match(html,/35%/);assert.match(html,/40%/);assert.match(html,/立即告警/);assert.match(html,/data-lsc-alert-config/);assert.match(html,/OC 上下线不播报/);
 assert.doesNotMatch(lscAlertRuleHTML(settings,false),/data-lsc-alert-config/);
});
test('invalid hysteresis and fractional values are rejected',()=>{
 for(const values of [{low_percent:30,recover_percent:30},{low_percent:0,recover_percent:25},{low_percent:20.5,recover_percent:25},{low_percent:20,recover_percent:25,low_duration_seconds:-1}])assert.throws(()=>lscAlertSettings(original,{low_duration_seconds:30,alerts:true,...values}));
});
