import { describe, expect, it } from "vitest";

import { isJwtExpired, readJwtClaims } from "./jwt";
import { makeJwt } from "./test-utils";

describe("readJwtClaims", () => {
  it("reads sub and exp without checking the signature", () => {
    expect(readJwtClaims(makeJwt({ sub: "merchant-a", exp: 1_800_000_000, aud: "tenant-console" }))).toEqual({
      sub: "merchant-a",
      exp: 1_800_000_000,
    });
  });

  it("decodes non-ASCII claims", () => {
    expect(readJwtClaims(makeJwt({ sub: "магазин-ä" }))?.sub).toBe("магазин-ä");
  });

  it("returns null for something that is not a JWT", () => {
    expect(readJwtClaims("mock")).toBeNull();
    expect(readJwtClaims("a.%%%.c")).toBeNull();
    expect(readJwtClaims(makeJwt({ sub: 42 }))).toBeNull();
  });
});

describe("isJwtExpired", () => {
  it("compares exp with now", () => {
    const token = makeJwt({ exp: 1000 });
    expect(isJwtExpired(token, 999_999)).toBe(false);
    expect(isJwtExpired(token, 1_000_000)).toBe(true);
  });

  it("treats a token without exp as not expired", () => {
    expect(isJwtExpired("mock", Date.now())).toBe(false);
  });
});
