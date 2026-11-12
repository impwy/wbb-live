// Toss Payments (test mode) bridge for Flutter Web. The SDK script is loaded
// only when a donation starts. requestPayment redirects to successUrl or
// failUrl, so a successful call does not resolve in this page.
(() => {
  const sdkUrl = 'https://js.tosspayments.com/v2/standard';
  let loading = null;
  function loadSdk() {
    if (window.TossPayments) return Promise.resolve();
    if (!loading) {
      loading = new Promise((resolve, reject) => {
        const script = document.createElement('script');
        script.src = sdkUrl;
        script.onload = () => resolve();
        script.onerror = () => { loading = null; reject(new Error('payment SDK unavailable')); };
        document.head.appendChild(script);
      });
    }
    return loading;
  }
  async function request(clientKey, json) {
    const o = JSON.parse(json);
    await loadSdk();
    const payment = window.TossPayments(clientKey).payment({ customerKey: o.customerKey });
    await payment.requestPayment({
      method: 'CARD',
      amount: { currency: 'KRW', value: o.amount },
      orderId: o.orderId,
      orderName: o.orderName,
      successUrl: o.successUrl,
      failUrl: o.failUrl,
    });
  }
  window.WbbPay = { request };
})();
