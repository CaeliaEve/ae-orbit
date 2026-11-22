// Cache Storage requires a secure context; IndexedDB also works on HTTP sites.
const CACHE_NAME = "ae-orbit-texture-packs-v1";
const CACHE_URL = "/__ae-orbit/local-texture-pack.zip";
const DB_NAME = "ae-orbit-texture-packs-v1";
const STORE_NAME = "archives";

function openDatabase(indexedDB) {
  return new Promise((resolve,reject)=>{
    const request=indexedDB.open(DB_NAME,1);
    let failed=false;
    request.onupgradeneeded=()=>{
      if(!request.result.objectStoreNames.contains(STORE_NAME))request.result.createObjectStore(STORE_NAME);
    };
    request.onerror=()=>reject(request.error||new Error("纹理缓存无法打开。"));
    request.onblocked=()=>{failed=true;reject(new Error("纹理缓存正在被其他页面占用。"));};
    request.onsuccess=()=>{if(failed){request.result.close();return;}resolve(request.result);};
  });
}

async function databaseRequest(indexedDB,mode,operation) {
  const db=await openDatabase(indexedDB);
  try {
    return await new Promise((resolve,reject)=>{
      const transaction=db.transaction(STORE_NAME,mode);
      const request=operation(transaction.objectStore(STORE_NAME));
      let result;
      request.onsuccess=()=>{result=request.result;};
      transaction.oncomplete=()=>resolve(result);
      transaction.onerror=transaction.onabort=()=>reject(transaction.error||request.error||new Error("纹理缓存操作失败。"));
    });
  } finally {db.close();}
}

export function createTextureArchiveCache({caches=globalThis.caches,indexedDB=globalThis.indexedDB}={}) {
  return {
    async save(bytes) {
      if(caches){
        try {
          const cache=await caches.open(CACHE_NAME);
          await cache.put(new Request(CACHE_URL),new Response(bytes,{headers:{"Content-Type":"application/zip"}}));
          // A stale HTTP fallback must not reappear after clearing the newer pack.
          if(indexedDB)await databaseRequest(indexedDB,"readwrite",store=>store.delete("local")).catch(()=>{});
          return true;
        } catch { /* The fallback handles HTTP, quota and unavailable caches. */ }
      }
      if(!indexedDB)return false;
      await databaseRequest(indexedDB,"readwrite",store=>store.put(new Blob([bytes],{type:"application/zip"}),"local"));
      return true;
    },
    async load() {
      // IndexedDB is authoritative when Cache Storage rejected the latest save.
      if(indexedDB){
        try {
          const value=await databaseRequest(indexedDB,"readonly",store=>store.get("local"));
          if(value instanceof Blob)return value.arrayBuffer();
        } catch { /* Preserve compatibility with previously cached archives. */ }
      }
      if(caches){
        try {
          const cache=await caches.open(CACHE_NAME),response=await cache.match(new Request(CACHE_URL));
          if(response)return await response.arrayBuffer();
        } catch { /* Use the HTTP fallback. */ }
      }
      return null;
    },
    async clear() {
      let cleared=false;
      if(caches){
        try {const cache=await caches.open(CACHE_NAME);await cache.delete(new Request(CACHE_URL));cleared=true;} catch {}
      }
      if(indexedDB){await databaseRequest(indexedDB,"readwrite",store=>store.delete("local"));cleared=true;}
      return cleared;
    },
  };
}
