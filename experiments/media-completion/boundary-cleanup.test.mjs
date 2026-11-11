import assert from 'node:assert/strict';
import {test} from 'node:test';
import {allowOwned,cleanupBoundary} from './boundary-cleanup.mjs';

function fixture(fail) {
  const calls=[];
  return {
    calls,
    args:{
      owned:new Set(['operation']),
      registeredPaths:new Map([['operation','wbb/test/1/source']]),
      armed:new Map([['before_register/operation',['before_register','operation']]]),
      pendingPosts:[Promise.resolve()],
      close:async()=>{calls.push('close');if(fail==='close')throw new Error('failed')},
      release:async()=>{calls.push('release');if(fail==='release')throw new Error('failed')},
      waitCompleted:async()=>{calls.push('completed')},
      revoke:async()=>{calls.push('revoke');if(fail==='revoke')throw new Error('failed')},
      assertAbsent:async()=>{calls.push('absence');if(fail==='absence')throw new Error('owned path remained')},
    },
  };
}

for(const stage of ['close','release','revoke','absence']){
  test(`cleanup ${stage} failure is fatal but other cleanup continues`,async()=>{
    const {args,calls}=fixture(stage);
    await assert.rejects(cleanupBoundary(args),/cleanup failed/);
    assert.deepEqual(calls,['close','release','completed','revoke','absence']);
  });
}

test('successful cleanup completes every owner check',async()=>{
  const {args,calls}=fixture();
  await cleanupBoundary(args);
  assert.deepEqual(calls,['close','release','completed','revoke','absence']);
});

test('lost allow response still closes and revokes the server-owned fixture',async()=>{
  const owned=new Set(),registeredPaths=new Map(),allowed=new Map(),calls=[];
  const id='operation',path='wbb/test/1/source';
  await assert.rejects(allowOwned(owned,registeredPaths,id,path,async()=>{
    allowed.set(id,path); // server committed, response was lost
    throw new Error('allow response lost');
  }),/allow response lost/);
  assert(owned.has(id) && registeredPaths.get(id)===path);
  await cleanupBoundary({owned,registeredPaths,armed:new Map(),pendingPosts:[],
    close:async()=>{calls.push('close')},
    release:async()=>assert.fail('no barrier'),
    waitCompleted:async()=>{calls.push('completed')},
    revoke:async(key,value)=>{calls.push('revoke');assert.equal(allowed.get(key),value);allowed.delete(key)},
    assertAbsent:async()=>{assert.equal(allowed.size,0);calls.push('absence')},
  });
  assert.deepEqual(calls,['close','completed','revoke','absence']);
});

test('lost allow response and revoke failure cannot finish successfully',async()=>{
  const owned=new Set(),registeredPaths=new Map();
  await assert.rejects(allowOwned(owned,registeredPaths,'operation','wbb/test/1/source',async()=>{
    throw new Error('allow response lost');
  }),/allow response lost/);
  await assert.rejects(cleanupBoundary({owned,registeredPaths,armed:new Map(),pendingPosts:[],
    close:async()=>{},release:async()=>{},waitCompleted:async()=>{},
    revoke:async()=>{throw new Error('revoke response lost')},assertAbsent:async()=>{},
  }),/cleanup failed: revoke/);
});
