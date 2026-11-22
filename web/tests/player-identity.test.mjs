import test from "node:test";
import assert from "node:assert/strict";
import {loginPayload, playerAccountHTML, playerName} from "../src/player-identity.mjs";
import {craftingJobModel, craftingJobsHTML} from "../src/crafting-center.mjs";
import {jobDetailHTML} from "../src/job-view.mjs";
import {createAPI} from "../src/api.mjs";

test("player registration preserves passwords and refuses mismatched credentials",()=>{
 const fields={gameID:" Player_A ",password:" some password ",confirmPassword:" some password ",invitation:"invite-code"};
 assert.deepEqual(loginPayload("register",fields),{path:"/api/v1/players/register",body:{game_id:"Player_A",password:" some password ",invitation:"invite-code"}});
 assert.throws(()=>loginPayload("register",{...fields,confirmPassword:"different"}),/不一致/);
 assert.throws(()=>loginPayload("player",{...fields,gameID:"admin<script>"}),/游戏 ID/);
 assert.deepEqual(loginPayload("token",{token:" old-token "}),{path:"/api/v1/session",body:{token:"old-token"}});
});

test("team controls require admin and escape player identity",()=>{
 const ordinary=playerAccountHTML({name:"old",game_id:"Player_A"},[]);
 assert.match(ordinary,/Player_A/);assert.doesNotMatch(ordinary,/生成邀请码/);
 const admin=playerAccountHTML({admin:true,name:"admin"},[],[{id:"member",name:"<script>alert(1)</script>"}]);
 assert.match(admin,/生成邀请码/);assert.doesNotMatch(admin,/<script>/);
 assert.equal(playerName({game_id:"Player_A",name:"player:player_a"}),"Player_A");
});

test("browser requests carry the current member guard without changing login requests",async()=>{
 const requests=[];
 const api=createAPI(async(path,options)=>{requests.push({path,...options});return {ok:true,status:200,json:async()=>({})};});
 api.setMember("member-a");api.setCSRF("csrf-token");
 await api.request("/api/v1/networks/net/jobs",{method:"POST",body:{}});
 await api.request("/api/v1/session",{method:"POST",body:{game_id:"Player_A",password:"password"},login:true});
 assert.equal(requests[0].headers["X-AE-Member"],"member-a");
 assert.equal(requests[1].headers["X-AE-Member"],undefined);
});

test("task names and actor history retain submitted identity while filtering by fixed member",()=>{
 const row={id:"job",owner_id:"member-a",owner_name:"Player_A",name:"test",control:"paused",counts:{pending:"1"},total:"1",created_at:"2026-11-20T00:00:00Z",updated_at:"2026-11-20T00:00:00Z"};
 const model={sync:{jobs:new Map([["job",row]])}};
 assert.equal(craftingJobModel(model,{memberId:"member-b",owner:"mine"}).rows.length,0);
 assert.equal(craftingJobModel(model,{search:"player_a"}).rows.length,1);
 assert.match(craftingJobsHTML(craftingJobModel(model),"unsettled",{dateTime:String,memberId:"member-a"}),/Player_A/);
 const html=jobDetailHTML({summary:row,entries:[],activity:[{actor_id:"admin",actor_name:"TeamAdmin",action:"pause",created_at:row.created_at}]},{controls:[]},{id:"member-b"},{itemFor:()=>null,itemImage:()=>"",quantity:String,dateTime:String,icon:()=>""});
 assert.match(html,/Player_A 下单/);assert.match(html,/TeamAdmin 暂停调度/);assert.match(html,/管理员代操作/);assert.doesNotMatch(html,/data-job-action=/);
});
