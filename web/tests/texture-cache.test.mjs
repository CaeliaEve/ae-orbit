import test from "node:test";
import assert from "node:assert/strict";
import { createTextureArchiveCache } from "../src/texture-cache.mjs";

function database({failWrites=false}={}) {
  const values=new Map();
  let closed=0;
  return {
    get closed(){return closed;},
    open(){
      const open={};
      const db={objectStoreNames:{contains:()=>true},close(){closed++;},transaction(){
        const transaction={};
        transaction.objectStore=()=>{
          const operation=(kind,key,value)=>{
            const request={};
            queueMicrotask(()=>{
              if(failWrites&&kind!=="get"){
                transaction.error=new Error("quota exceeded");transaction.onabort?.();return;
              }
              if(kind==="put")values.set(key,value);
              if(kind==="delete")values.delete(key);
              request.result=values.get(key);request.onsuccess?.();
              queueMicrotask(()=>transaction.oncomplete?.());
            });return request;
          };
          return {put:(v,k)=>operation("put",k,v),get:k=>operation("get",k),delete:k=>operation("delete",k)};
        };
        return transaction;
      }};
      open.result=db;queueMicrotask(()=>open.onsuccess?.());return open;
    },
  };
}

test("HTTP fallback preserves binary ZIP across cache instances and clears it",async()=>{
  const indexedDB=database(),bytes=new Uint8Array([80,75,3,4,0,255]);
  const first=createTextureArchiveCache({caches:null,indexedDB});
  assert.equal(await first.save(bytes),true);
  const restored=await createTextureArchiveCache({caches:null,indexedDB}).load();
  assert.deepEqual(new Uint8Array(restored),bytes);
  await first.clear();assert.equal(await first.load(),null);
  assert.equal(indexedDB.closed,4);
});

test("unavailable Cache Storage falls back to IndexedDB and does not replace newer data with an old cache",async()=>{
  const indexedDB=database();
  const cache=createTextureArchiveCache({caches:{open:async()=>{throw Error("unavailable");}},indexedDB});
  const bytes=new Uint8Array([80,75,1]);await cache.save(bytes);
  const stale={open:async()=>({match:async()=>new Response(new Uint8Array([9]))})};
  assert.deepEqual(new Uint8Array(await createTextureArchiveCache({caches:stale,indexedDB}).load()),bytes);
});

test("quota failure rejects persistence while absence of both storage APIs returns false",async()=>{
  const indexedDB=database({failWrites:true});
  await assert.rejects(createTextureArchiveCache({caches:null,indexedDB}).save(new Uint8Array([1])),/quota/);
  assert.equal(indexedDB.closed,1);
  const none=createTextureArchiveCache({caches:null,indexedDB:null});
  assert.equal(await none.save(new Uint8Array([1])),false);assert.equal(await none.load(),null);assert.equal(await none.clear(),false);
});
