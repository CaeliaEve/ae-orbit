export class APIError extends Error {
  constructor(message, { status = 0, code = "network_error", uncertain = false, jobIds = [], retryAfter = null } = {}) {
    super(message);
    this.status = status;
    this.code = code;
    this.uncertain = uncertain;
    this.jobIds = jobIds;
    this.retryAfter = retryAfter;
  }
}

const messages = {
  unauthorized: "会话已失效，请重新登录。",
  forbidden: "当前账号没有此操作权限。",
  capability_unavailable: "当前网络或 OC 不支持此操作。",
  capacity_exceeded: "队列已满，请稍后再提交。",
  conflict: "状态已变化，请重新核对。",
  view_changed: "目录已变化，请从第一页重新加载。",
  not_found: "未找到对应记录。",
  invalid_request: "提交内容无效，请检查数量和物品。",
  busy: "后端忙，请稍后核对。",
  timeout: "请求超时，受理结果待核对。",
  invalid_reference: "物品或网络已变化，请重新核对。",
  json_required: "请求格式不受支持。",
  body_too_large: "请求内容过大。",
  dev_backend_unavailable: "开发后端尚未连接，请检查服务地址。",
};

export function createAPI(fetcher = globalThis.fetch.bind(globalThis)) {
  let csrf = "";
  return {
    setCSRF(value) { csrf = typeof value === "string" ? value : ""; },
    async request(path, { method = "GET", body, signal, login = false } = {}) {
      const mutation = method !== "GET" && method !== "HEAD";
      if (mutation && !login && !csrf) throw new APIError("会话需要重新验证。", { status: 401 });
      const headers = { Accept: "application/json" };
      if (body !== undefined) headers["Content-Type"] = "application/json";
      if (mutation && !login) headers["X-CSRF-Token"] = csrf;
      let response;
      try {
        response = await fetcher(path, {
          method, headers, credentials: "same-origin", signal,
          ...(body === undefined ? {} : { body: JSON.stringify(body) }),
        });
      } catch (error) {
        if (error.name === "AbortError" && !mutation) throw error;
        throw new APIError(mutation ? "连接中断，受理结果待核对。" : "无法连接后端。", { uncertain: mutation });
      }
      if (response.status === 204) return null;
      let result;
      try { result = await response.json(); }
      catch {
        throw new APIError("后端返回了无法识别的响应。", {
          status: response.status, uncertain: mutation,
        });
      }
      if (!response.ok) {
        const code = typeof result?.error?.code === "string" ? result.error.code : "request_failed";
        const jobIds = Array.isArray(result?.error?.job_ids)
          ? [...new Set(result.error.job_ids.slice(0, 500).filter(id => typeof id === "string" && /^[A-Za-z0-9_.:-]{1,128}$/.test(id)))] : [];
        const retryHeader = response.headers?.get("Retry-After");
        const retryAfter = typeof retryHeader === "string" && /^[0-9]{1,6}$/.test(retryHeader)
          ? Number(retryHeader) : null;
        throw new APIError(messages[code] || "请求未完成，请核对状态。", {
          status: response.status, code, jobIds, retryAfter,
          uncertain: mutation && (!Object.hasOwn(messages, code) || response.status >= 500 || [408, 425, 499].includes(response.status) || code === "conflict"),
        });
      }
      return result;
    },
  };
}
