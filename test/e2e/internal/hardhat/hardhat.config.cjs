const { extendEnvironment } = require("hardhat/config");

function parseRules() {
  const raw = process.env.HARDHAT_E2E_RULES;
  if (!raw) return [];
  try {
    const parsed = JSON.parse(raw);
    return Array.isArray(parsed) ? parsed : [];
  } catch (err) {
    throw new Error(`invalid HARDHAT_E2E_RULES: ${err.message}`);
  }
}

extendEnvironment((hre) => {
  let rules = parseRules();
  const requests = [];
  const provider = hre.network.provider;
  const originalRequest = provider.request.bind(provider);

  provider.request = async (args) => {
    const method = args && args.method;
    const params = args && Object.prototype.hasOwnProperty.call(args, "params") ? args.params : [];

    if (method === "hardhat_getE2ERequests") {
      return requests.slice();
    }
    if (method === "hardhat_clearE2ERequests") {
      requests.length = 0;
      return true;
    }
    if (method === "hardhat_setE2ERules") {
      rules = Array.isArray(params) && Array.isArray(params[0]) ? params[0] : [];
      return true;
    }

    requests.push({ method, params, ts: Date.now(), seq: requests.length + 1 });

    const rule = rules.find((candidate) => {
      if (!candidate || candidate.method !== method) return false;
      if (!Object.prototype.hasOwnProperty.call(candidate, "params")) return true;
      return JSON.stringify(candidate.params) === JSON.stringify(params);
    });
    if (rule) {
      if (rule.delayMs) {
        await new Promise((resolve) => setTimeout(resolve, Number(rule.delayMs)));
      }
      if (Object.prototype.hasOwnProperty.call(rule, "result")) {
        return rule.result;
      }
      if (rule.error) {
        const error = new Error(rule.error.message || "scripted hardhat e2e error");
        error.code = rule.error.code || -32000;
        error.data = rule.error.data;
        throw error;
      }
    }

    return originalRequest(args);
  };
});

const forkingUrl = process.env.HARDHAT_FORK_URL;
if (!forkingUrl) {
  throw new Error("HARDHAT_FORK_URL is required for nodecore e2e Hardhat fork");
}

const forkingBlock = process.env.HARDHAT_FORK_BLOCK ? Number(process.env.HARDHAT_FORK_BLOCK) : undefined;

module.exports = {
  networks: {
    hardhat: {
      chainId: 1,
      forking: {
        url: forkingUrl,
        ...(forkingBlock ? { blockNumber: forkingBlock } : {}),
      },
    },
  },
};
