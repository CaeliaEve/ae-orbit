export const pluginEscape=value=>String(value??'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
const paths={
 energy:'<rect x="2" y="7" width="17" height="10" rx="2"/><path d="M22 11v2M11 8l-3 5h5l-2 4"/>',
 subnet:'<rect x="9" y="2" width="6" height="6" rx="1"/><rect x="2" y="16" width="6" height="6" rx="1"/><rect x="16" y="16" width="6" height="6" rx="1"/><path d="M12 8v4M5 16v-4h14v4"/>',
 qq:'<path d="M21 11.5a8.4 8.4 0 0 1-1 4 8.5 8.5 0 0 1-7.5 4 8.4 8.4 0 0 1-4-1L3 21l2.5-5.5a8.4 8.4 0 0 1-1-4A8.5 8.5 0 0 1 13 3h.5a8.5 8.5 0 0 1 7.5 8v.5Z"/><path d="M8 11h8M8 14h5"/>',
};
export function pluginSymbol(kind){return `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${paths[kind]||paths.subnet}</svg>`;}
export function pluginCardHTML({kind,title,description,features=[],enabled=false,status='',actions=''}){
 return `<article class="market-plugin plugin-catalog-entry" data-plugin-kind="${kind}"><div class="plugin-card-top"><span class="market-plugin-symbol">${pluginSymbol(kind)}</span><span class="plugin-state" data-tone="${enabled?'ok':'idle'}">${enabled?'已启用':'未启用'}</span></div><h2>${pluginEscape(title)}</h2><p class="plugin-description">${pluginEscape(description)}</p><div class="plugin-feature-list">${features.map(f=>`<span>${pluginEscape(f)}</span>`).join('')}</div><div class="plugin-card-bottom"><small>${pluginEscape(status)}</small><div class="energy-actions">${actions}</div></div></article>`;
}
