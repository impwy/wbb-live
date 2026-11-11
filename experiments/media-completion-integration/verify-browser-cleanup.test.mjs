import test from 'node:test';
import assert from 'node:assert/strict';
import {launchOwnedPair} from './verify-browser.mjs';

function server(name,closed){
  const process={exitCode:0,signalCode:null};
  return {wsEndpoint:()=>name,process:()=>process,close:async()=>{closed.push(name)}};
}

test('second Chrome launch failure closes first owner',async()=>{
  const closed=[];let count=0;
  const fake={launchServer:async()=>++count===1?server('publisher',closed):Promise.reject(new Error('viewer launch failed'))};
  await assert.rejects(launchOwnedPair(fake,[]),/viewer launch failed/);
  assert.deepEqual(closed,['publisher']);
});

test('second Chrome connect failure closes both owners',async()=>{
  const closed=[];let count=0;
  const fake={launchServer:async()=>server(++count===1?'publisher':'viewer',closed),
    connect:async endpoint=>endpoint==='viewer'?Promise.reject(new Error('viewer connect failed')):{}};
  await assert.rejects(launchOwnedPair(fake,[]),/viewer connect failed/);
  assert.deepEqual(closed,['viewer','publisher']);
});
