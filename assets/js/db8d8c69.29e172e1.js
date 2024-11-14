"use strict";(self.webpackChunkdocs=self.webpackChunkdocs||[]).push([[398],{2490:(e,t,n)=>{n.r(t),n.d(t,{assets:()=>u,contentTitle:()=>c,default:()=>p,frontMatter:()=>s,metadata:()=>l,toc:()=>h});var r=n(5893),o=n(1151),i=n(9965),a=n(4996);const s={sidebar_position:2},c="Architecture",l={id:"arch",title:"Architecture",description:"InterLink aims to provide an abstraction for the execution of a Kubernetes pod on any remote resource capable of managing a Container execution lifecycle.",source:"@site/docs/arch.mdx",sourceDirName:".",slug:"/arch",permalink:"/interLink/docs/arch",draft:!1,unlisted:!1,editUrl:"https://github.com/interTwin-eu/interLink/docs/arch.mdx",tags:[],version:"current",sidebarPosition:2,frontMatter:{sidebar_position:2},sidebar:"tutorialSidebar",previous:{title:"Introduction",permalink:"/interLink/docs/intro"},next:{title:"Cookbook",permalink:"/interLink/docs/Cookbook"}},u={},h=[];function d(e){const t={a:"a",h1:"h1",li:"li",p:"p",strong:"strong",ul:"ul",...(0,o.a)(),...e.components};return(0,r.jsxs)(r.Fragment,{children:[(0,r.jsx)(t.h1,{id:"architecture",children:"Architecture"}),"\n",(0,r.jsx)(t.p,{children:"InterLink aims to provide an abstraction for the execution of a Kubernetes pod on any remote resource capable of managing a Container execution lifecycle."}),"\n",(0,r.jsx)(t.p,{children:"The project consists of two main components:"}),"\n",(0,r.jsxs)(t.ul,{children:["\n",(0,r.jsxs)(t.li,{children:[(0,r.jsx)(t.strong,{children:"A Kubernetes Virtual Node:"})," based on the ",(0,r.jsx)(t.a,{href:"https://virtual-kubelet.io/",children:"VirtualKubelet"})," technology. Translating request for a kubernetes pod execution into a remote call to the interLink API server."]}),"\n",(0,r.jsxs)(t.li,{children:[(0,r.jsx)(t.strong,{children:"The interLink API server:"})," a modular and pluggable REST server where you can create your own Container manager plugin (called sidecars), or use the existing ones: remote docker execution on a remote host, singularity Container on a remote SLURM batch system."]}),"\n"]}),"\n",(0,r.jsxs)(t.p,{children:["The project got inspired by the ",(0,r.jsx)(t.a,{href:"https://github.com/CARV-ICS-FORTH/knoc",children:"KNoC"})," and ",(0,r.jsx)(t.a,{href:"https://github.com/liqotech/liqo/tree/master",children:"Liqo"})," projects, enhancing that with the implemention a generic API layer b/w the virtual kubelet component and the provider logic for the container lifecycle management."]}),"\n",(0,r.jsx)(i.Z,{alt:"Docusaurus themed image",sources:{light:(0,a.Z)("/img/InterLink_excalidraw_light.svg"),dark:(0,a.Z)("/img/InterLink_excalidraw-dark.svg")}})]})}function p(e={}){const{wrapper:t}={...(0,o.a)(),...e.components};return t?(0,r.jsx)(t,{...e,children:(0,r.jsx)(d,{...e})}):d(e)}},1151:(e,t,n)=>{n.d(t,{Z:()=>s,a:()=>a});var r=n(7294);const o={},i=r.createContext(o);function a(e){const t=r.useContext(i);return r.useMemo((function(){return"function"==typeof e?e(t):{...t,...e}}),[t,e])}function s(e){let t;return t=e.disableParentContext?"function"==typeof e.components?e.components(o):e.components||o:a(e.components),r.createElement(i.Provider,{value:t},e.children)}}}]);