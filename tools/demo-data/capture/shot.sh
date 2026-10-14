#!/bin/bash
# Captura una URL de Grafana en una pestana NUEVA (reutilizar la misma pagina
# cuelga en la segunda navegacion) y la guarda a 1x de escala, para que el ancho
# real del PNG sea el del viewport y no el doble por deviceScaleFactor.
node -e '
const fs=require("fs");const sleep=ms=>new Promise(r=>setTimeout(r,ms));
(async()=>{
  const url=process.argv[1],out=process.argv[2],w=+process.argv[3],h=+process.argv[4];
  const t=await (await fetch("http://127.0.0.1:9222/json/new?about:blank",{method:"PUT"})).json();
  const ws=new WebSocket(t.webSocketDebuggerUrl);
  await new Promise(r=>ws.addEventListener("open",r,{once:true}));
  let id=0;const pend=new Map();
  ws.addEventListener("message",e=>{const m=JSON.parse(e.data);if(m.id&&pend.has(m.id)){const{resolve}=pend.get(m.id);pend.delete(m.id);resolve(m.result);}});
  const send=(me,pa={})=>new Promise(res=>{const mid=++id;pend.set(mid,{resolve:res});ws.send(JSON.stringify({id:mid,method:me,params:pa}));});
  await send("Page.enable");await send("Runtime.enable");await send("Network.enable");
  await send("Network.setExtraHTTPHeaders",{headers:{Authorization:"Basic "+Buffer.from("admin:changeme").toString("base64")}});
  await send("Emulation.setDeviceMetricsOverride",{width:w,height:h,deviceScaleFactor:1,mobile:false});
  await send("Page.navigate",{url});await sleep(18000);
  const s=await send("Page.captureScreenshot",{format:"png"});
  fs.writeFileSync(out,Buffer.from(s.data,"base64"));
  console.log("OK "+out.split("/").pop()+" "+Math.round(s.data.length*0.75/1024)+"KB");
  await fetch("http://127.0.0.1:9222/json/close/"+t.id);
  process.exit(0);
})();' "$@"