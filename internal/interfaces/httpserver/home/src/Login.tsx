import { useState } from 'react';

// Login is the email-OTP gate (plan M3). Request a code, then verify it; the
// server sets an HttpOnly session cookie on success.
export function Login({ onSignedIn }: { onSignedIn: () => void }) {
  const [email, setEmail] = useState('');
  const [challengeID, setChallengeID] = useState('');
  const [code, setCode] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  const request = async () => {
    setBusy(true);
    setError('');
    try {
      const response = await fetch('/v1/auth/otp/request', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ email }),
      });
      if (!response.ok) throw new Error(`HTTP ${response.status}`);
      const body = await response.json();
      if (body.challenge_id) setChallengeID(body.challenge_id);
      else setError('如果该邮箱在白名单中，验证码已发送'); // uniform response
      if (body.challenge_id === undefined) setChallengeID(''); // stay on step 1
      if (body.challenge_id) setError('');
      if (body.challenge_id) return;
    } catch (e) {
      setError(`发送失败：${e instanceof Error ? e.message : e}`);
    } finally {
      setBusy(false);
    }
  };

  const verify = async () => {
    setBusy(true);
    setError('');
    try {
      const response = await fetch('/v1/auth/otp/verify', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ challenge_id: challengeID, code }),
      });
      if (!response.ok) throw new Error('验证码错误或已过期');
      onSignedIn();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="login">
      <h1>membox remote</h1>
      {!challengeID ? (
        <>
          <p>输入白名单邮箱，收取一次性验证码。</p>
          <input
            type="email"
            placeholder="you@example.com"
            value={email}
            autoFocus
            onChange={(e) => setEmail(e.target.value)}
            onKeyDown={(e) => e.key === 'Enter' && !busy && request()}
          />
          <button disabled={busy || !email.includes('@')} onClick={request}>
            发送验证码
          </button>
        </>
      ) : (
        <>
          <p>验证码已发送（10 分钟内有效，最多 5 次尝试）。</p>
          <input
            inputMode="numeric"
            placeholder="6 位验证码"
            value={code}
            autoFocus
            onChange={(e) => setCode(e.target.value)}
            onKeyDown={(e) => e.key === 'Enter' && !busy && verify()}
          />
          <button disabled={busy || code.length !== 6} onClick={verify}>
            登录
          </button>
        </>
      )}
      {error && <p className="login-error">{error}</p>}
    </div>
  );
}
