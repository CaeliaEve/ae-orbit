import test from 'node:test';import assert from 'node:assert/strict';
import {energyValue,energyPercent,energyAggregate,energyChart,energyTrend} from '../src/plugin-market.mjs';
test('energy stays exact beyond int64 and aggregate is capacity weighted',()=>{
 const a='123456789012345678901234567890';assert.equal(energyValue(a),123456789012345678901234567890n);
 assert.equal(energyPercent(a,'246913578024691357802469135780'),50);
 const sum=energyAggregate([{status:'online',stored_eu:'10',capacity_eu:'100'},{status:'online',stored_eu:'900',capacity_eu:'1000'},{status:'offline',stored_eu:'9999',capacity_eu:'10000'}]);assert.equal(sum.percent,82.72);assert.equal(sum.count,2);
 assert.equal(energyValue('1e30'),null);assert.equal(energyValue('01'),null);
});
test('chart segments epochs and never interprets a restart as continuous trend',()=>{
 const points=[{at:'2026-11-21T00:00:00Z',stored_eu:'100',capacity_eu:'1000',epoch:1},{at:'2026-11-21T00:00:30Z',stored_eu:'200',capacity_eu:'1000',epoch:1},{at:'2026-11-21T00:01:00Z',stored_eu:'300',capacity_eu:'1000',epoch:2}];
 assert.equal((energyChart(points).match(/<polyline/g)||[]).length,2);assert.equal(energyTrend(points),null);
 points[2].epoch=1;assert.equal(energyTrend(points).perSecond,'3');
});
