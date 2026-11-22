import test from 'node:test';
import assert from 'node:assert/strict';
import {integrationBase,integrationInput,integrationExample,integrationKeyState} from '../src/integration-settings.mjs';

test('API grants are explicit and scoped to the selected data',()=>{
 const f=new FormData();f.set('name',' Tool ');f.append('scope','inventory.read');f.append('scope','subnet.read');f.append('network','net');f.append('group','sh');f.set('expires_days','90');
 assert.deepEqual(integrationInput(f),{name:'Tool',scopes:['inventory.read','subnet.read'],network_ids:['net'],server_groups:['sh'],enabled:true,revision:0,expires_days:90});
 f.delete('network');assert.throws(()=>integrationInput(f));f.append('network','net');f.delete('scope');f.append('scope','subnet.read');assert.deepEqual(integrationInput(f).network_ids,[]);
});
test('integration examples use current origin and never a real secret',()=>{
 assert.equal(integrationBase('http://example:8080/anything'),'http://example:8080/api/integration/v1');
 assert.match(integrationExample('http://example:8080'),/Bearer YOUR_API_KEY/);
 const now=Date.parse('2026-11-22T10:00:00Z');assert.equal(integrationKeyState({enabled:true,expires_at:'2026-11-22T09:00:00Z'},now),'已到期');assert.equal(integrationKeyState({enabled:false,expires_at:'2027-01-01'},now),'已停用');
});
