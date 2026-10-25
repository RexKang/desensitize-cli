package report

// reportCSS：左栏总览 + 目录，右栏分段明细。
const reportCSS = `
body{margin:0;font:13.5px/1.6 system-ui,"Microsoft YaHei",sans-serif;color:#1f2328;background:#fff;
     display:grid;grid-template-columns:322px minmax(0,1fr);overflow:hidden}
#nav{height:100vh;overflow-y:auto;border-right:1px solid #e3e8ee;background:#fbfcfd;padding:14px 12px 30px}
.tl{font-size:16px;font-weight:700;margin:0}
.tsub{color:#6e7781;font-size:11.5px;margin:2px 0 8px}
.tstat{font-size:12px;color:#3d4451;margin-bottom:10px}
.tstat b{color:#cf222e;font-size:13.5px;font-variant-numeric:tabular-nums}
table.mini{width:100%;border-collapse:collapse;font-size:11.8px;margin:0 0 12px}
table.mini th,table.mini td{padding:3px 4px;text-align:left;border-bottom:1px solid #eef1f4}
table.mini th{color:#57606a;font-weight:600;border-bottom:1px solid #dfe4ea}
table.mini td.n{text-align:right;font-variant-numeric:tabular-nums}
.mini-h{font-size:11px;color:#8b949e;margin:0 0 4px;letter-spacing:.04em}
#q{width:100%;padding:5px 8px;border:1px solid #d0d7de;border-radius:6px;font:inherit;font-size:12px}
#hint{display:block;font-size:11px;color:#8b949e;margin:4px 0 6px;min-height:14px}
.toc{border-top:1px solid #eef1f4;padding-top:6px}
.toc a{display:block;text-decoration:none;color:#1f2328;padding:4px 7px;border-radius:6px;
       border-left:2px solid transparent;margin-bottom:1px}
.toc a:hover{background:#eef2f6}
.toc a.on{background:#e7f0fb;border-left-color:#0969da}
.toc .nm{display:block;font-weight:600;font-size:12.5px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.toc .pa{display:block;color:#96a0ab;font-size:11px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.toc .cn{color:#8b949e;font-weight:400;font-size:11px;margin-left:5px}
#main{height:100vh;overflow-y:auto;padding:16px 26px 70vh;position:relative;scroll-behavior:smooth}
section.sec{padding:2px 0 12px;border-bottom:1px solid #eef1f4}
section.sec h2{font-size:14.5px;margin:12px 0 2px;color:#0a3069;word-break:break-all;font-weight:600}
.dsum{color:#8b949e;font-size:11.5px;margin-bottom:6px}
.file{margin:0 0 12px}
.file h3{font-size:12.8px;margin:10px 0 5px;font-family:ui-monospace,Consolas,monospace;font-weight:600;color:#3d4451}
.file h3 span{color:#8b949e;font-weight:400;margin-left:8px;font-family:inherit}
table.d{border-collapse:collapse;width:100%}
table.d th,table.d td{border:1px solid #d8dee4;padding:4px 8px;text-align:left;vertical-align:top}
table.d th{background:#f6f8fa;font-size:11.5px;color:#57606a;font-weight:600}
table.d tr:nth-child(even) td{background:#fbfcfd}
.rule{white-space:nowrap;color:#6639ba}
.loc{font-family:ui-monospace,Consolas,monospace;font-size:11.5px;color:#0a3069;white-space:nowrap}
.val{font-family:ui-monospace,Consolas,monospace;font-size:11.5px;color:#cf222e;white-space:pre-wrap;word-break:break-all}
.val.out{color:#1a7f37}
.ctx{font-size:11.5px;color:#3d4451;white-space:pre-wrap;word-break:break-all;max-width:720px}
mark{background:#ffd8d3;color:#82071e;font-weight:600;padding:0 1px;border-radius:2px}
.num{text-align:right;white-space:nowrap;font-variant-numeric:tabular-nums}
a.open{color:inherit;text-decoration:none;border-bottom:1px dashed #c4ccd4}
a.open:hover{color:#0969da;border-bottom-color:#0969da}
a.open.dir{font-size:11.5px;font-weight:400;color:#6e7781;margin-left:8px}
.warn{color:#9a6700;background:#fff8c5;border:1px solid #eed888;padding:6px 10px;border-radius:6px;
      font-size:12px;margin:0 0 10px;max-width:760px}
@media(max-width:900px){body{grid-template-columns:1fr}#nav{display:none}}
`

// reportJS 负责：中文目录按拼音重排、目录点击跳转、滚动高亮、关键词筛选。
const reportJS = `
<script>
const nav=document.getElementById('nav');
const main=document.getElementById('main');
const links=new Map();
document.querySelectorAll('.toc a[data-sec]').forEach(a=>links.set(a.dataset.sec,a));
[...document.querySelectorAll('.toc a[data-sec]')]
  .map(a=>({a:a,s:document.getElementById(a.getAttribute('data-sec')),k:a.getAttribute('title')||''}))
  .sort((x,y)=>x.k.localeCompare(y.k,'zh-Hans-CN',{numeric:true}))
  .forEach(p=>{document.querySelector('.toc').appendChild(p.a); if(p.s)main.appendChild(p.s);});
const secs=[...document.querySelectorAll('section.sec')];
let active=null;
function setActive(id){
  if(active===id)return;
  const prev=links.get(active); if(prev)prev.classList.remove('on');
  active=id;
  const a=links.get(id);
  if(!a)return;
  a.classList.add('on');
  const r=a.getBoundingClientRect(), n=nav.getBoundingClientRect();
  if(r.top<n.top+6||r.bottom>n.bottom-6)a.scrollIntoView({block:'nearest'});
}
function visible(){return secs.filter(s=>s.style.display!=='none');}
function spy(){
  const vis=visible();
  if(!vis.length)return;
  const top=main.scrollTop+96;
  let pick=vis[0];
  for(const s of vis){ if(s.offsetTop<=top)pick=s; else break; }
  setActive(pick.id);
}
let raf=0;
main.addEventListener('scroll',()=>{ if(raf)return; raf=requestAnimationFrame(()=>{spy();raf=0;}); },{passive:true});
links.forEach((a,id)=>a.addEventListener('click',e=>{
  e.preventDefault();
  const t=document.getElementById(id);
  if(!t)return;
  main.scrollTo({top:t.offsetTop-6,behavior:'smooth'});
  setActive(id);
}));
const q=document.getElementById('q');
q.addEventListener('input',()=>{
  const v=q.value.trim().toLowerCase();
  secs.forEach(s=>{
    const hit=!v||s.dataset.s.indexOf(v)>=0;
    s.style.display=hit?'':'none';
    const a=links.get(s.id);
    if(a)a.parentElement.style.display=hit?'':'none';
  });
  document.getElementById('hint').textContent=v?(visible().length+' 个目录命中'):'';
  main.scrollTop=0; spy();
});
spy();
</script>
`
