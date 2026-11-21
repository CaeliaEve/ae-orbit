import test from 'node:test';
import assert from 'node:assert/strict';
import { availableNavigation, normalizeNavigation, partitionNavigation, loadNavigation, saveNavigation, moveNavigation, placeNavigation, orderedNavigation } from '../src/navigation.mjs';

test('only enabled reviewed plugins create routes, with new plugins in More', () => {
  const available = availableNavigation([{enabled:true,plugin:{id:'gtnh.lsc'}},{enabled:true,plugin:{id:'untrusted'}}]);
  assert.ok(available.some(row=>row.id==='plugin-lsc'));
  assert.equal(available.length,9);
  assert.equal(orderedNavigation(null,available).at(-1).placement,'drawer');
  assert.equal(availableNavigation([{enabled:false,plugin:{id:'gtnh.lsc'}}]).length,8);
});
test('automatic overflow preserves user order and never mutates preferences', () => {
  const preferences=normalizeNavigation(null),before=JSON.stringify(preferences);
  const small=partitionNavigation(preferences,availableNavigation(),2);
  assert.deepEqual(small.primary.map(row=>row.id),['dashboard','inventory']);
  assert.deepEqual(small.automatic,['history','orders','cpus','network','plugins']);
  assert.equal(small.drawer.length,6);
  assert.equal(JSON.stringify(preferences),before);
  assert.equal(partitionNavigation(preferences,availableNavigation(),20).primary.length,7);
});
test('member preferences stay isolated and disabled plugin positions survive', () => {
  const data=new Map(),storage={getItem:key=>data.get(key),setItem:(key,value)=>data.set(key,value)};
  const enabled=availableNavigation([{enabled:true,plugin:{id:'gtnh.lsc'}}]);
  let preferences=placeNavigation(null,'plugin-lsc','primary');
  preferences=moveNavigation(preferences,'plugin-lsc',-1,enabled);
  saveNavigation(storage,'alice',preferences);
  assert.equal(loadNavigation(storage,'bob').entries.at(-1).placement,'drawer');
  const reloaded=loadNavigation(storage,'alice');
  assert.equal(orderedNavigation(reloaded,enabled).at(-2).id,'plugin-lsc');
  assert.ok(!orderedNavigation(reloaded,availableNavigation()).some(row=>row.id==='plugin-lsc'));
  assert.equal(orderedNavigation(reloaded,enabled).at(-2).placement,'primary');
});
test('invalid saved rows cannot hide settings, inject routes, or duplicate buttons', () => {
  const normalized=normalizeNavigation({entries:[{id:'settings',placement:'drawer'},{id:'inventory',placement:'drawer'},{id:'inventory'},{id:'javascript:bad'}]});
  assert.equal(normalized.entries.length,10);
  assert.deepEqual(normalized.entries[0],{id:'inventory',placement:'drawer'});
  assert.ok(!normalized.entries.some(row=>row.id==='settings'));
});
test('reordering visible entries skips disabled plugins without dropping their preference', () => {
  const before=normalizeNavigation({entries:[{id:'dashboard'},{id:'plugin-lsc',placement:'primary'},{id:'inventory'}]});
  const after=moveNavigation(before,'inventory',-1,availableNavigation());
  assert.deepEqual(after.entries.slice(0,3).map(row=>row.id),['inventory','plugin-lsc','dashboard']);
  assert.deepEqual(moveNavigation(after,'inventory',-1,availableNavigation()),after);
});
