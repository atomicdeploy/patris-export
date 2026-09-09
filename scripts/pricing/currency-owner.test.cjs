const test = require('node:test');
const assert = require('node:assert/strict');
const { runCurrency } = require('./currency-owner.cjs');
const options = { requestId:'currency-proof-01', values:{yuan_price:'34500'}, key:'ck_'+'a'.repeat(40), secret:'cs_'+'b'.repeat(40) };
const state = { success:true, data:{state_revision:'sha256:'+'c'.repeat(64),yuan_price:34500} };
const job = {success:true,data:{job_id:'d'.repeat(32),generation:1,request_id:options.requestId,status:'confirmed',desired_currency:{yuan_price:34500}}};
test('send only requested fields and retain exact owner admission headers', async()=>{
 const calls=[];
 const result=await runCurrency({...options,requestJSON:async(base,path,args)=>{calls.push({path,args});return args.body?job:state;}});
 assert.equal(result.delivered,true);
 assert.deepEqual(calls[1].args.body,{yuan_price:'34500',request_id:options.requestId,expected_state_revision:state.data.state_revision});
 assert.equal(calls[1].args.identityHeaders['If-Match'],'"'+state.data.state_revision+'"');
 assert.equal(calls[1].args.identityHeaders['Idempotency-Key'],options.requestId);
 assert.equal(calls.length,3);
});
test('uncertain POST is never retried',async()=>{
 let writes=0;
 const result=await runCurrency({...options,requestJSON:async(base,path,args)=>{if(args.body){writes++;throw Error('request_timeout');}return state;}});
 assert.equal(writes,1);assert.equal(result.delivered,false);assert.equal(result.outcome,'unknown_delivery_outcome');assert.equal(result.request_id,options.requestId);
});
test('status recovery sends only GET and verifies current owner values',async()=>{
 const calls=[];
 const result=await runCurrency({...options,values:{},observeOnly:true,requestJSON:async(base,path,args)=>{calls.push(args);return path.includes('/requests/')?job:state;}});
 assert.equal(result.delivered,true);assert.ok(calls.every(c=>c.body===undefined));
});
test('changed owner readback cannot be reported as delivered',async()=>{
 let reads=0; const events=[];
 const result=await runCurrency({...options,onEvent:e=>events.push(e),requestJSON:async(base,path,args)=>args.body?job:(++reads===1?state:{success:true,data:{...state.data,yuan_price:35000}})});
 assert.equal(result.delivered,false);assert.equal(result.error,'owner_readback_changed');
 assert.ok(events.some(e=>e.phase==='verifying_owner_readback'));
 assert.ok(events.every(e=>e.delivered===false && !Object.hasOwn(e,'percent')));
});
test('progress distinguishes confirmed owner job from verified delivery',async()=>{
 const events=[];
 const result=await runCurrency({...options,onEvent:e=>events.push(e),requestJSON:async(base,path,args)=>args.body?job:state});
 assert.equal(result.delivered,true);
 assert.deepEqual(events.map(e=>e.phase),['reading_owner','submitting','verifying_owner_readback','confirmed']);
 assert.ok(events.slice(0,-1).every(e=>!e.delivered));
 assert.equal(events.at(-1).delivered,true);
 assert.equal(events.at(-1).job_id,job.data.job_id);
});
