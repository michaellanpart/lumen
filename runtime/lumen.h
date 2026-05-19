// Lumen v0.2 C runtime. Tiny, header-only, no external deps beyond libc + pthread.
#ifndef LUMEN_RT_H
#define LUMEN_RT_H

#include <inttypes.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <ctype.h>
#include <unistd.h>

// Opaque function-pointer carrier for first-class Lumen fn values.
// Per ISO C, casts between two function-pointer types are well-defined as
// long as the value is only invoked through a pointer matching its real
// signature. Runtime builtins that accept Lumen handlers declare the
// concrete signature and cast on entry.
typedef void (*Lm_FnPtr)(void);
typedef struct { void *fn; void *env; } Lm_Closure;

// Lock-free output buffer used by the print primitives when the program
// opts in via io_setbuf(...). Bypasses stdio entirely (no locks, no format
// parsing) — gives Go/Rust-class throughput on tight write loops.
static char  *Lm__obuf      = NULL;
static size_t Lm__obuf_cap  = 0;
static size_t Lm__obuf_len  = 0;

static void Lm__obuf_flush_all(void) {
    if (Lm__obuf_len > 0) {
        ssize_t _ = write(1, Lm__obuf, Lm__obuf_len);
        (void)_;
        Lm__obuf_len = 0;
    }
}

static inline void Lm__obuf_write(const char *p, size_t n) {
    if (Lm__obuf_len + n > Lm__obuf_cap) {
        Lm__obuf_flush_all();
        if (n > Lm__obuf_cap) {
            ssize_t _ = write(1, p, n);
            (void)_;
            return;
        }
    }
    memcpy(Lm__obuf + Lm__obuf_len, p, n);
    Lm__obuf_len += n;
}

// Hand-rolled itoa (printf("%lld") is ~6x slower because of format parsing).
static inline void Lm_print_i64(int64_t v) {
    char b[24];
    char *p = b + sizeof b;
    int neg = (v < 0);
    uint64_t u = neg ? (uint64_t)(-(v + 1)) + 1u : (uint64_t)v;
    do { *--p = (char)('0' + (u % 10)); u /= 10; } while (u);
    if (neg) *--p = '-';
    size_t n = (size_t)(b + sizeof b - p);
    if (Lm__obuf) { Lm__obuf_write(p, n); return; }
    fwrite(p, 1, n, stdout);
}
static inline void Lm_print_f64(double v) {
    if (Lm__obuf) {
        char b[32];
        int n = snprintf(b, sizeof b, "%g", v);
        if (n > 0) Lm__obuf_write(b, (size_t)n);
        return;
    }
    printf("%g", v);
}
static inline void Lm_print_bool(bool v) {
    if (Lm__obuf) { Lm__obuf_write(v ? "true" : "false", v ? 4 : 5); return; }
    fputs(v ? "true" : "false", stdout);
}
static inline void Lm_print_str(const char *v) {
    size_t n = strlen(v);
    if (Lm__obuf) { Lm__obuf_write(v, n); return; }
    fwrite(v, 1, n, stdout);
}
static inline void Lm_print_sp(void) {
    if (Lm__obuf) { Lm__obuf_write(" ", 1); return; }
    fputc(' ', stdout);
}
static inline void Lm_print_nl(void) {
    if (Lm__obuf) { Lm__obuf_write("\n", 1); return; }
    fputc('\n', stdout);
}

// Builtin: io_setbuf(size_bytes) — install a lock-free output buffer of
// `size` bytes, bypassing stdio for all subsequent print* calls in this
// process. An atexit handler flushes the tail. Idempotent.
static inline void Lm_io_setbuf(int64_t size) {
    if (size <= 0 || Lm__obuf != NULL) return;
    Lm__obuf = (char *)malloc((size_t)size);
    if (Lm__obuf == NULL) return;
    Lm__obuf_cap = (size_t)size;
    atexit(Lm__obuf_flush_all);
}

// ---------------------------------------------------------------------------
// Vec<T>: growable heap array. Lm_Vec is the runtime representation.
// Typed push/pop/get helpers are generated per element type.
// ---------------------------------------------------------------------------
typedef struct {
    void   *data;
    size_t  len;
    size_t  cap;
    size_t  elem_size;
} Lm_Vec;

static inline Lm_Vec Lm_vec_new(size_t elem_size) {
    return (Lm_Vec){ .data = NULL, .len = 0, .cap = 0, .elem_size = elem_size };
}

static inline void Lm__vec_grow(Lm_Vec *v) {
    size_t newcap = v->cap == 0 ? 8 : v->cap * 2;
    v->data = realloc(v->data, newcap * v->elem_size);
    v->cap = newcap;
}

static inline void Lm_vec_push_raw(Lm_Vec *v, const void *elem) {
    if (v->len == v->cap) Lm__vec_grow(v);
    memcpy((char*)v->data + v->len * v->elem_size, elem, v->elem_size);
    v->len++;
}

static inline void* Lm_vec_get_raw(const Lm_Vec *v, int64_t i) {
    return (char*)v->data + (size_t)i * v->elem_size;
}

static inline void* Lm_vec_pop_raw(Lm_Vec *v) {
    if (v->len == 0) { fprintf(stderr, "lumen: Vec::pop on empty Vec\n"); exit(1); }
    v->len--;
    return (char*)v->data + v->len * v->elem_size;
}

static inline int64_t Lm_vec_len(const Lm_Vec *v) { return (int64_t)v->len; }

/* i64 */
static inline void    Lm_vec_push_i64(Lm_Vec *v, int64_t x) { Lm_vec_push_raw(v, &x); }
static inline int64_t Lm_vec_get_i64 (const Lm_Vec *v, int64_t i) { return *(int64_t*)Lm_vec_get_raw(v, i); }
static inline int64_t Lm_vec_pop_i64 (Lm_Vec *v) { return *(int64_t*)Lm_vec_pop_raw(v); }

/* f64 */
static inline void   Lm_vec_push_f64(Lm_Vec *v, double x) { Lm_vec_push_raw(v, &x); }
static inline double Lm_vec_get_f64 (const Lm_Vec *v, int64_t i) { return *(double*)Lm_vec_get_raw(v, i); }
static inline double Lm_vec_pop_f64 (Lm_Vec *v) { return *(double*)Lm_vec_pop_raw(v); }

/* bool (stored as int64_t for uniform sizing) */
static inline void    Lm_vec_push_bool(Lm_Vec *v, int64_t x) { Lm_vec_push_raw(v, &x); }
static inline int64_t Lm_vec_get_bool (const Lm_Vec *v, int64_t i) { return *(int64_t*)Lm_vec_get_raw(v, i); }
static inline int64_t Lm_vec_pop_bool (Lm_Vec *v) { return *(int64_t*)Lm_vec_pop_raw(v); }

/* String (const char*) */
static inline void         Lm_vec_push_str(Lm_Vec *v, const char *x) { Lm_vec_push_raw(v, &x); }
static inline const char*  Lm_vec_get_str (const Lm_Vec *v, int64_t i) { return *(const char**)Lm_vec_get_raw(v, i); }
static inline const char*  Lm_vec_pop_str (Lm_Vec *v) { return *(const char**)Lm_vec_pop_raw(v); }

// ---------------------------------------------------------------------------
// String helpers: Lm_str_cat_s/i/f/b, Lm_fmt
// ---------------------------------------------------------------------------
#include <stdarg.h>

// Concatenate two strings. Returns a malloc-allocated C string.
static inline const char *Lm_str_cat_s(const char *a, const char *b) {
    size_t la = a ? strlen(a) : 0, lb = b ? strlen(b) : 0;
    char *out = (char *)malloc(la + lb + 1);
    if (!out) return "";
    memcpy(out, a ? a : "", la);
    memcpy(out + la, b ? b : "", lb);
    out[la + lb] = '\0';
    return out;
}

// Concatenate a string with an i64. Returns malloc-allocated C string.
static inline const char *Lm_str_cat_i(const char *a, int64_t n) {
    char buf[32];
    snprintf(buf, sizeof(buf), "%" PRId64, n);
    return Lm_str_cat_s(a, buf);
}

// Concatenate a string with an f64.
static inline const char *Lm_str_cat_f(const char *a, double n) {
    char buf[48];
    snprintf(buf, sizeof(buf), "%g", n);
    return Lm_str_cat_s(a, buf);
}

// Concatenate a string with a bool.
static inline const char *Lm_str_cat_b(const char *a, int64_t b) {
    return Lm_str_cat_s(a, b ? "true" : "false");
}

// ---------------------------------------------------------------------------
// String methods: contains, starts_with, ends_with, trim, to_upper,
//                 to_lower, slice, replace, index_of, split
// All allocating variants return malloc-allocated C strings.
// ---------------------------------------------------------------------------

static inline bool Lm_str_starts_with(const char *s, const char *prefix) {
    if (!s || !prefix) return false;
    size_t pl = strlen(prefix);
    return strncmp(s, prefix, pl) == 0;
}

static inline bool Lm_str_ends_with(const char *s, const char *suffix) {
    if (!s || !suffix) return false;
    size_t sl = strlen(s), xl = strlen(suffix);
    if (xl > sl) return false;
    return strcmp(s + sl - xl, suffix) == 0;
}

// Trim leading and trailing ASCII whitespace. Returns malloc-allocated string.
static inline const char *Lm_str_trim(const char *s) {
    if (!s) return "";
    while (*s == ' ' || *s == '\t' || *s == '\n' || *s == '\r') s++;
    size_t len = strlen(s);
    while (len > 0 && (s[len-1] == ' ' || s[len-1] == '\t' ||
                        s[len-1] == '\n' || s[len-1] == '\r')) len--;
    char *out = (char *)malloc(len + 1);
    if (!out) return "";
    memcpy(out, s, len);
    out[len] = '\0';
    return out;
}

// Returns malloc-allocated uppercase copy.
static inline const char *Lm_str_to_upper(const char *s) {
    if (!s) return "";
    size_t n = strlen(s);
    char *out = (char *)malloc(n + 1);
    if (!out) return "";
    for (size_t i = 0; i < n; i++) out[i] = (char)toupper((unsigned char)s[i]);
    out[n] = '\0';
    return out;
}

// Returns malloc-allocated lowercase copy.
static inline const char *Lm_str_to_lower(const char *s) {
    if (!s) return "";
    size_t n = strlen(s);
    char *out = (char *)malloc(n + 1);
    if (!out) return "";
    for (size_t i = 0; i < n; i++) out[i] = (char)tolower((unsigned char)s[i]);
    out[n] = '\0';
    return out;
}

// Slice [start, end) — clamps to actual length. Returns malloc-allocated string.
static inline const char *Lm_str_slice(const char *s, int64_t start, int64_t end) {
    if (!s) return "";
    int64_t n = (int64_t)strlen(s);
    if (start < 0) start = 0;
    if (end > n) end = n;
    if (start > end) start = end;
    size_t len = (size_t)(end - start);
    char *out = (char *)malloc(len + 1);
    if (!out) return "";
    memcpy(out, s + start, len);
    out[len] = '\0';
    return out;
}

// Replace all non-overlapping occurrences of `from` with `to`.
// Returns malloc-allocated string.
static inline const char *Lm_str_replace(const char *s, const char *from, const char *to) {
    if (!s || !from || !*from) return s ? s : "";
    size_t fl = strlen(from), tl = strlen(to);
    size_t cap = strlen(s) + 64, len = 0;
    char *out = (char *)malloc(cap);
    if (!out) return "";
    const char *p = s;
    while (*p) {
        const char *hit = strstr(p, from);
        if (!hit) {
            size_t rest = strlen(p);
            while (len + rest + 1 > cap) { cap *= 2; char *t = (char *)realloc(out, cap); if (t) out = t; }
            memcpy(out + len, p, rest);
            len += rest;
            break;
        }
        size_t prefix = (size_t)(hit - p);
        while (len + prefix + tl + 1 > cap) { cap *= 2; char *t = (char *)realloc(out, cap); if (t) out = t; }
        memcpy(out + len, p, prefix); len += prefix;
        memcpy(out + len, to, tl);   len += tl;
        p = hit + fl;
    }
    out[len] = '\0';
    return out;
}

// Lm_str_index_of and Lm_str_split are defined after Lm_Option/Lm_Vec below.

// ---------------------------------------------------------------------------
// Lm_fmt(template, nargs, tag0, val0, tag1, val1, ...)
//
// Replaces each {} in template with the formatted value for the next arg.
// {{ and }} are escape sequences that emit a literal { or } respectively.
// Tags: 's' (String/const char*), 'i' (i64), 'f' (f64), 'b' (bool as i64).
// Returns a malloc-allocated C string.
// ---------------------------------------------------------------------------
static inline const char *Lm_fmt(const char *tmpl, int nargs, ...) {
    va_list ap;
    size_t cap = strlen(tmpl) + 64;
    char *out = (char *)malloc(cap);
    if (!out) return "";
    size_t len = 0;

#define LM_FMT_GROW(need) \
    while (len + (need) + 1 > cap) { cap *= 2; char *_t = (char *)realloc(out, cap); if (_t) out = _t; }
#define LM_FMT_PUTC(c) do { LM_FMT_GROW(1); out[len++] = (char)(c); } while(0)

    va_start(ap, nargs);
    const char *p = tmpl;
    int arg_i = 0;
    while (*p) {
        if (p[0] == '{' && p[1] == '{') {
            LM_FMT_PUTC('{'); p += 2; continue;
        }
        if (p[0] == '}' && p[1] == '}') {
            LM_FMT_PUTC('}'); p += 2; continue;
        }
        if (p[0] == '{' && p[1] == '}' && arg_i < nargs) {
            p += 2;
            char buf[64];
            int tag = va_arg(ap, int);
            uintptr_t raw = va_arg(ap, uintptr_t);
            arg_i++;
            switch ((char)tag) {
            case 's': {
                const char *s = (const char *)raw;
                size_t sl = s ? strlen(s) : 0;
                LM_FMT_GROW(sl);
                if (s) { memcpy(out + len, s, sl); len += sl; }
                break;
            }
            case 'i': {
                int wn = snprintf(buf, sizeof(buf), "%" PRId64, (int64_t)raw);
                LM_FMT_GROW((size_t)wn);
                memcpy(out + len, buf, (size_t)wn); len += (size_t)wn;
                break;
            }
            case 'f': {
                double d; memcpy(&d, &raw, sizeof(d));
                int wn = snprintf(buf, sizeof(buf), "%g", d);
                LM_FMT_GROW((size_t)wn);
                memcpy(out + len, buf, (size_t)wn); len += (size_t)wn;
                break;
            }
            case 'b': {
                const char *bs = raw ? "true" : "false";
                size_t bl = strlen(bs);
                LM_FMT_GROW(bl);
                memcpy(out + len, bs, bl); len += bl;
                break;
            }
            }
            continue;
        }
        LM_FMT_PUTC(*p); p++;
    }
    va_end(ap);

#undef LM_FMT_GROW
#undef LM_FMT_PUTC

    out[len] = '\0';
    return out;
}

// ---------------------------------------------------------------------------
// Option<T> and Result<T, E> — erased-union runtime representation.
//
// The Lumen type checker tracks concrete element/ok/err types; the C backend
// accesses the correct union member. Both types are plain C structs (Copy).
// ---------------------------------------------------------------------------

typedef union {
    int64_t     i;   /* KI64 / KBool */
    double      f;   /* KF64         */
    const char *s;   /* KString      */
} Lm__Val;

typedef struct {
    bool     present;
    Lm__Val  val;
} Lm_Option;

typedef struct {
    bool     is_ok;
    Lm__Val  ok;
    Lm__Val  err;
} Lm_Result;

// Option::unwrap — panics if None.
static inline Lm__Val Lm__option_unwrap(Lm_Option o) {
    if (!o.present) {
        fprintf(stderr, "lumen: Option::unwrap called on None\n");
        exit(1);
    }
    return o.val;
}

// Result::unwrap — panics if Err.
static inline Lm__Val Lm__result_unwrap(Lm_Result r) {
    if (!r.is_ok) {
        fprintf(stderr, "lumen: Result::unwrap called on Err\n");
        exit(1);
    }
    return r.ok;
}

// Result::unwrap_err — panics if Ok.
static inline Lm__Val Lm__result_unwrap_err(Lm_Result r) {
    if (r.is_ok) {
        fprintf(stderr, "lumen: Result::unwrap_err called on Ok\n");
        exit(1);
    }
    return r.err;
}

// ---------------------------------------------------------------------------
// parse_int(s: String) -> Option<i64>
//
// Tries to parse s as a decimal integer. Returns Option::None on failure
// (NULL input, empty string, trailing garbage, or overflow).
// ---------------------------------------------------------------------------
#include <errno.h>

static inline Lm_Option Lm_parse_int(const char *s) {
    if (!s || !*s) return (Lm_Option){ .present = false };
    char *end;
    errno = 0;
    long long v = strtoll(s, &end, 10);
    if (*end != '\0' || errno != 0) return (Lm_Option){ .present = false };
    return (Lm_Option){ .present = true, .val.i = (int64_t)v };
}

// ---------------------------------------------------------------------------
// String methods that depend on Lm_Option / Lm_Vec (defined above).
// ---------------------------------------------------------------------------

// Returns Option<i64>: Some(index) if sub is found, None otherwise.
static inline Lm_Option Lm_str_index_of(const char *s, const char *sub) {
    if (!s || !sub) return (Lm_Option){ .present = false };
    const char *hit = strstr(s, sub);
    if (!hit) return (Lm_Option){ .present = false };
    return (Lm_Option){ .present = true, .val.i = (int64_t)(hit - s) };
}

// Split s by sep; returns a Lm_Vec of const char* (each part malloc-allocated).
static inline Lm_Vec Lm_str_split(const char *s, const char *sep) {
    Lm_Vec vec = Lm_vec_new(sizeof(const char *));
    if (!s) return vec;
    if (!sep || !*sep) {
        const char *copy = s;
        Lm_vec_push_str(&vec, copy);
        return vec;
    }
    size_t sl = strlen(sep);
    const char *p = s;
    while (1) {
        const char *hit = strstr(p, sep);
        if (!hit) {
            size_t n = strlen(p);
            char *part = (char *)malloc(n + 1);
            if (part) { memcpy(part, p, n); part[n] = '\0'; }
            Lm_vec_push_str(&vec, part ? part : "");
            break;
        }
        size_t n = (size_t)(hit - p);
        char *part = (char *)malloc(n + 1);
        if (part) { memcpy(part, p, n); part[n] = '\0'; }
        Lm_vec_push_str(&vec, part ? part : "");
        p = hit + sl;
    }
    return vec;
}

// ---------------------------------------------------------------------------
// Builtin: HashMap<K, V> — open-addressing hash table with linear probing.
// Keys and values are stored as Lm__Val (the same tagged union used by
// Option/Result). The hash function is FNV-1a applied to the raw bytes.
// ---------------------------------------------------------------------------

#define LM_HASHMAP_INIT_CAP 16
#define LM_HASHMAP_LOAD_NUM 3
#define LM_HASHMAP_LOAD_DEN 4   /* resize when fill > 75% */

typedef struct {
    Lm__Val key;
    Lm__Val val;
    bool    used;
} Lm__HMEntry;

typedef struct {
    Lm__HMEntry *entries;
    int64_t      cap;
    int64_t      len;
} Lm_HashMap;

static inline uint64_t Lm__fnv1a(const void *data, size_t n) {
    uint64_t h = UINT64_C(14695981039346656037);
    const uint8_t *p = (const uint8_t *)data;
    for (size_t i = 0; i < n; i++) {
        h ^= (uint64_t)p[i];
        h *= UINT64_C(1099511628211);
    }
    return h;
}

/* Hash an Lm__Val. For strings we hash the characters; for others the bits. */
static inline uint64_t Lm__val_hash(Lm__Val v) {
    if (v.s) {
        /* Treat as string if the pointer field might be set. We detect
         * non-null s to decide: if i (the int64) is non-null as a ptr we
         * still need a stable hash, so hash the int bits. The caller's
         * valMember tag determines how the value is used at the type level;
         * we pick a consistent policy: hash the 8-byte block. */
        return Lm__fnv1a(&v, sizeof v);
    }
    return Lm__fnv1a(&v, sizeof v);
}

/* Two Lm__Vals are equal when all bits match (works for i64/f64/bool/ptr). */
static inline bool Lm__val_eq(Lm__Val a, Lm__Val b) {
    return memcmp(&a, &b, sizeof a) == 0;
}

static inline Lm_HashMap Lm_hashmap_new(void) {
    Lm_HashMap m;
    m.cap     = LM_HASHMAP_INIT_CAP;
    m.len     = 0;
    m.entries = (Lm__HMEntry *)calloc((size_t)m.cap, sizeof(Lm__HMEntry));
    return m;
}

static inline void Lm__hashmap_insert_raw(Lm_HashMap *m, Lm__Val key, Lm__Val val) {
    uint64_t h = Lm__val_hash(key);
    int64_t  i = (int64_t)(h & (uint64_t)(m->cap - 1));
    while (m->entries[i].used && !Lm__val_eq(m->entries[i].key, key))
        i = (i + 1) & (m->cap - 1);
    if (!m->entries[i].used) m->len++;
    m->entries[i].key  = key;
    m->entries[i].val  = val;
    m->entries[i].used = true;
}

static inline void Lm__hashmap_grow(Lm_HashMap *m) {
    int64_t       old_cap = m->cap;
    Lm__HMEntry  *old     = m->entries;
    m->cap     = old_cap * 2;
    m->len     = 0;
    m->entries = (Lm__HMEntry *)calloc((size_t)m->cap, sizeof(Lm__HMEntry));
    for (int64_t i = 0; i < old_cap; i++)
        if (old[i].used)
            Lm__hashmap_insert_raw(m, old[i].key, old[i].val);
    free(old);
}

static inline void Lm_hashmap_insert(Lm_HashMap *m, Lm__Val key, Lm__Val val) {
    if (m->len * LM_HASHMAP_LOAD_DEN >= m->cap * LM_HASHMAP_LOAD_NUM)
        Lm__hashmap_grow(m);
    Lm__hashmap_insert_raw(m, key, val);
}

/* Returns Lm_Option: present=true with the value, or present=false. */
static inline Lm_Option Lm_hashmap_get(const Lm_HashMap *m, Lm__Val key) {
    uint64_t h = Lm__val_hash(key);
    int64_t  i = (int64_t)(h & (uint64_t)(m->cap - 1));
    int64_t  checked = 0;
    while (m->entries[i].used && checked < m->cap) {
        if (Lm__val_eq(m->entries[i].key, key))
            return (Lm_Option){ .present = true, .val = m->entries[i].val };
        i = (i + 1) & (m->cap - 1);
        checked++;
    }
    return (Lm_Option){ .present = false };
}

static inline bool Lm_hashmap_contains(const Lm_HashMap *m, Lm__Val key) {
    return Lm_hashmap_get(m, key).present;
}

/* remove returns Lm_Option with the removed value, or None if not found. */
static inline Lm_Option Lm_hashmap_remove(Lm_HashMap *m, Lm__Val key) {
    uint64_t h = Lm__val_hash(key);
    int64_t  i = (int64_t)(h & (uint64_t)(m->cap - 1));
    int64_t  checked = 0;
    while (m->entries[i].used && checked < m->cap) {
        if (Lm__val_eq(m->entries[i].key, key)) {
            Lm_Option result = (Lm_Option){ .present = true, .val = m->entries[i].val };
            /* Tombstone-free deletion: shift subsequent entries back. */
            int64_t j = (i + 1) & (m->cap - 1);
            while (m->entries[j].used) {
                Lm__HMEntry e = m->entries[j];
                m->entries[j].used = false;
                m->len--;
                Lm__hashmap_insert_raw(m, e.key, e.val);
                j = (j + 1) & (m->cap - 1);
            }
            m->entries[i].used = false;
            m->len--;
            return result;
        }
        i = (i + 1) & (m->cap - 1);
        checked++;
    }
    return (Lm_Option){ .present = false };
}

static inline int64_t Lm_hashmap_len(const Lm_HashMap *m) { return m->len; }

// ---------------------------------------------------------------------------
// Builtin: http_serve(host, port, body)
//
// Blocks forever serving HTTP/1.1 "200 OK"+body to every connection on
// host:port. One pthread per connection, with keep-alive and pipelining.
// Identical wire shape to benchmarks/servers/c/server.c so the load
// generator can hit it the same way.
// ---------------------------------------------------------------------------
#include <arpa/inet.h>
#include <errno.h>
#include <netinet/in.h>
#include <netinet/tcp.h>
#include <pthread.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <sys/types.h>
#include <sys/uio.h>
#include <unistd.h>

typedef struct {
    int fd;
    const char *resp;
    size_t resp_len;
} Lm_http_conn;

// Per-thread response is sent via writev so N pipelined replies collapse to a
// single syscall. macOS caps writev at IOV_MAX (1024); we batch in chunks.
#ifndef LM_HTTP_IOV_MAX
#define LM_HTTP_IOV_MAX 1024
#endif

static void *Lm_http_handle(void *arg) {
    Lm_http_conn *c = (Lm_http_conn *)arg;
    int fd = c->fd;
    const char *resp = c->resp;
    size_t resp_len = c->resp_len;
    free(c);

    struct iovec iov[LM_HTTP_IOV_MAX];
    for (int i = 0; i < LM_HTTP_IOV_MAX; i++) {
        iov[i].iov_base = (void *)resp;
        iov[i].iov_len  = resp_len;
    }

    char buf[16384];
    for (;;) {
        ssize_t n = recv(fd, buf, sizeof buf, 0);
        if (n <= 0) break;

        // Crude request counter: number of "\r\n\r\n" markers in this batch.
        int reqs = 0;
        for (ssize_t i = 3; i < n; i++) {
            if (buf[i-3] == '\r' && buf[i-2] == '\n' &&
                buf[i-1] == '\r' && buf[i]   == '\n') reqs++;
        }
        if (reqs == 0) reqs = 1;

        // One writev per (up to) LM_HTTP_IOV_MAX responses.
        while (reqs > 0) {
            int chunk = reqs > LM_HTTP_IOV_MAX ? LM_HTTP_IOV_MAX : reqs;
            ssize_t w = writev(fd, iov, chunk);
            if (w < 0) {
                if (errno == EINTR) continue;
                goto done;
            }
            reqs -= chunk;
        }
    }
done:
    close(fd);
    return NULL;
}

static inline void Lm_http_serve(const char *host, int64_t port, const char *body) {
    // Build the canonical response once.
    size_t body_len = strlen(body);
    char *resp;
    int resp_len = asprintf(&resp,
        "HTTP/1.1 200 OK\r\n"
        "Content-Type: text/plain\r\n"
        "Content-Length: %zu\r\n"
        "Connection: keep-alive\r\n"
        "\r\n"
        "%s",
        body_len, body);
    if (resp_len < 0) { fprintf(stderr, "http_serve: oom\n"); exit(1); }

    int s = socket(AF_INET, SOCK_STREAM, 0);
    if (s < 0) { perror("socket"); exit(1); }
    int one = 1;
    setsockopt(s, SOL_SOCKET, SO_REUSEADDR, &one, sizeof one);

    struct sockaddr_in addr = {0};
    addr.sin_family = AF_INET;
    addr.sin_port = htons((uint16_t)port);
    inet_pton(AF_INET, host, &addr.sin_addr);
    if (bind(s, (struct sockaddr*)&addr, sizeof addr) < 0) { perror("bind"); exit(1); }
    if (listen(s, 1024) < 0) { perror("listen"); exit(1); }

    fprintf(stderr, "lumen: listening on %s:%" PRId64 "\n", host, port);

    for (;;) {
        int fd = accept(s, NULL, NULL);
        if (fd < 0) { if (errno == EINTR) continue; perror("accept"); break; }
        int nd = 1;
        setsockopt(fd, IPPROTO_TCP, TCP_NODELAY, &nd, sizeof nd);
        Lm_http_conn *c = (Lm_http_conn *)malloc(sizeof *c);
        c->fd = fd;
        c->resp = resp;
        c->resp_len = (size_t)resp_len;
        pthread_t t;
        if (pthread_create(&t, NULL, Lm_http_handle, c) != 0) {
            close(fd);
            free(c);
            continue;
        }
        pthread_detach(t);
    }
}

// ---------------------------------------------------------------------------
// Builtin: http_serve_fn(host, port, handler, svc)
//
// Like http_serve, but invokes a user-supplied Lumen handler per request.
// `handler` is a `fn(&Service) String` and `svc` is a `&Service` pointer.
// The runtime hands the same opaque `svc` pointer to every handler call;
// the borrow checker has already proven that this sharing is safe (handlers
// receive `&Service`, not `&mut Service`).
//
// Response shape: hard-coded "HTTP/1.1 200 OK" with the handler-returned
// body. Each response is built into a per-connection scratch buffer and
// flushed once per recv batch via writev so pipelining still coalesces
// syscalls. No iovec[1024]-with-shared-body shortcut: each response may
// differ.
// ---------------------------------------------------------------------------
typedef const char *(*Lm_HttpHandler)(const void *, void *);

typedef struct {
    int fd;
    Lm_HttpHandler handler;
    const void *svc;
} Lm_http_fn_conn;

static void *Lm_http_fn_handle(void *arg) {
    Lm_http_fn_conn *c = (Lm_http_fn_conn *)arg;
    int fd = c->fd;
    Lm_HttpHandler handler = c->handler;
    const void *svc = c->svc;
    free(c);

    // Per-conn scratch: read buffer + response coalescing buffer.
    char rbuf[16384];
    // The response buffer grows on demand but starts large enough for
    // typical pipelined batches (16 responses x ~256 bytes each).
    size_t wcap = 16384;
    char *wbuf = (char *)malloc(wcap);
    if (!wbuf) { close(fd); return NULL; }

    static const char hdr[] =
        "HTTP/1.1 200 OK\r\n"
        "Content-Type: text/plain\r\n"
        "Content-Length: ";
    static const char tail[] = "\r\nConnection: keep-alive\r\n\r\n";

    for (;;) {
        ssize_t n = recv(fd, rbuf, sizeof rbuf, 0);
        if (n <= 0) break;

        // Count complete requests in this batch (terminator "\r\n\r\n").
        int reqs = 0;
        for (ssize_t i = 3; i < n; i++) {
            if (rbuf[i-3] == '\r' && rbuf[i-2] == '\n' &&
                rbuf[i-1] == '\r' && rbuf[i]   == '\n') reqs++;
        }
        if (reqs == 0) reqs = 1;

        size_t wlen = 0;
        for (int r = 0; r < reqs; r++) {
            const char *body = handler(svc, NULL);
            size_t blen = body ? strlen(body) : 0;

            // Reserve worst-case: hdr + 20 digits + tail + body.
            size_t need = wlen + (sizeof hdr - 1) + 20 + (sizeof tail - 1) + blen;
            if (need > wcap) {
                while (need > wcap) wcap *= 2;
                char *nw = (char *)realloc(wbuf, wcap);
                if (!nw) goto done;
                wbuf = nw;
            }

            memcpy(wbuf + wlen, hdr, sizeof hdr - 1);
            wlen += sizeof hdr - 1;
            // itoa Content-Length
            char nb[24];
            char *p = nb + sizeof nb;
            uint64_t u = (uint64_t)blen;
            do { *--p = (char)('0' + (u % 10)); u /= 10; } while (u);
            size_t nlen = (size_t)(nb + sizeof nb - p);
            memcpy(wbuf + wlen, p, nlen);
            wlen += nlen;
            memcpy(wbuf + wlen, tail, sizeof tail - 1);
            wlen += sizeof tail - 1;
            if (blen) {
                memcpy(wbuf + wlen, body, blen);
                wlen += blen;
            }
        }

        // One write() per recv batch.
        size_t off = 0;
        while (off < wlen) {
            ssize_t w = write(fd, wbuf + off, wlen - off);
            if (w < 0) { if (errno == EINTR) continue; goto done; }
            off += (size_t)w;
        }
    }
done:
    free(wbuf);
    close(fd);
    return NULL;
}

static inline void Lm_http_serve_fn(const char *host, int64_t port,
                                    Lm_Closure handler_clos, const void *svc) {
    Lm_HttpHandler handler = (Lm_HttpHandler)handler_clos.fn;

    int s = socket(AF_INET, SOCK_STREAM, 0);
    if (s < 0) { perror("socket"); exit(1); }
    int one = 1;
    setsockopt(s, SOL_SOCKET, SO_REUSEADDR, &one, sizeof one);

    struct sockaddr_in addr = {0};
    addr.sin_family = AF_INET;
    addr.sin_port = htons((uint16_t)port);
    inet_pton(AF_INET, host, &addr.sin_addr);
    if (bind(s, (struct sockaddr*)&addr, sizeof addr) < 0) { perror("bind"); exit(1); }
    if (listen(s, 1024) < 0) { perror("listen"); exit(1); }

    fprintf(stderr, "lumen: listening on %s:%" PRId64 " (handler mode)\n", host, port);

    for (;;) {
        int fd = accept(s, NULL, NULL);
        if (fd < 0) { if (errno == EINTR) continue; perror("accept"); break; }
        int nd = 1;
        setsockopt(fd, IPPROTO_TCP, TCP_NODELAY, &nd, sizeof nd);
        Lm_http_fn_conn *c = (Lm_http_fn_conn *)malloc(sizeof *c);
        c->fd = fd;
        c->handler = handler;
        c->svc = svc;
        pthread_t t;
        if (pthread_create(&t, NULL, Lm_http_fn_handle, c) != 0) {
            close(fd);
            free(c);
            continue;
        }
        pthread_detach(t);
    }
}

// ---------------------------------------------------------------------------
// Web framework: Request/Response types + http_serve_req(host, port, handler)
//
// Handlers receive a fully-parsed Lm_Request (method, path, query, body)
// and return an Lm_Response (status, body, content_type). The runtime
// formats a proper HTTP/1.1 reply for each request, supporting keep-alive
// pipelining and correct status codes.
//
// Usage in Lumen:
//   func handle(req: Request) -> Response {
//       if req.path == "/" { return Response::ok("hello\n") }
//       Response::with_status(404, "not found\n")
//   }
//   func main() { http_serve_req("127.0.0.1", 8080, handle) }
// ---------------------------------------------------------------------------

typedef struct {
    const char *method;
    const char *path;
    const char *query;
    const char *body;
} Lm_Request;

typedef struct {
    int64_t     status;
    const char *body;
    const char *content_type;
} Lm_Response;

static inline Lm_Response Lm_Response_ok(const char *body) {
    Lm_Response r;
    r.status       = 200;
    r.body         = body ? body : "";
    r.content_type = "text/plain; charset=utf-8";
    return r;
}

static inline Lm_Response Lm_Response_with_status(int64_t status, const char *body) {
    Lm_Response r;
    r.status       = status;
    r.body         = body ? body : "";
    r.content_type = "text/plain; charset=utf-8";
    return r;
}

static inline Lm_Response Lm_Response_json(const char *body) {
    Lm_Response r;
    r.status       = 200;
    r.body         = body ? body : "";
    r.content_type = "application/json; charset=utf-8";
    return r;
}

static inline Lm_Response Lm_Response_json_status(int64_t status, const char *body) {
    Lm_Response r;
    r.status       = status;
    r.body         = body ? body : "";
    r.content_type = "application/json; charset=utf-8";
    return r;
}

typedef Lm_Response (*Lm_ReqHandlerFn)(Lm_Request, void *);

typedef struct {
    int            fd;
    Lm_ReqHandlerFn handler;
} Lm_http_req_conn;

/* Map numeric status to a reason phrase. */
static const char *Lm__status_text(int64_t s) {
    switch (s) {
    case 200: return "OK";
    case 201: return "Created";
    case 204: return "No Content";
    case 301: return "Moved Permanently";
    case 302: return "Found";
    case 400: return "Bad Request";
    case 401: return "Unauthorized";
    case 403: return "Forbidden";
    case 404: return "Not Found";
    case 405: return "Method Not Allowed";
    case 409: return "Conflict";
    case 422: return "Unprocessable Entity";
    case 500: return "Internal Server Error";
    case 502: return "Bad Gateway";
    case 503: return "Service Unavailable";
    default:  return "Unknown";
    }
}

static void *Lm_http_req_handle(void *arg) {
    Lm_http_req_conn *c = (Lm_http_req_conn *)arg;
    int fd = c->fd;
    Lm_ReqHandlerFn handler = c->handler;
    free(c);

    /* Read buffer: large enough for typical headers + small bodies. */
    char rbuf[65536];
    size_t rlen = 0;

    /* Write buffer: grows on demand. */
    size_t wcap = 32768;
    char *wbuf = (char *)malloc(wcap);
    if (!wbuf) { close(fd); return NULL; }

    for (;;) {
        /* Append incoming bytes to rbuf. */
        ssize_t n = recv(fd, rbuf + rlen, sizeof rbuf - 1 - rlen, 0);
        if (n <= 0) break;
        rlen += (size_t)n;
        rbuf[rlen] = '\0';

        size_t wlen = 0;

        /* Process all complete requests in the buffer. */
        size_t consumed = 0;
        while (consumed < rlen) {
            char *base = rbuf + consumed;
            size_t avail = rlen - consumed;

            /* Find end-of-headers marker. */
            char *hdr_end = NULL;
            for (size_t i = 0; i + 3 < avail; i++) {
                if (base[i]=='\r' && base[i+1]=='\n' &&
                    base[i+2]=='\r' && base[i+3]=='\n') {
                    hdr_end = base + i;
                    break;
                }
            }
            if (!hdr_end) break; /* need more data */

            /* ---- Parse request line ---- */
            char method_buf[16]  = {0};
            char path_buf[2048]  = {0};
            char query_buf[2048] = {0};

            char *line_end = base;
            while (line_end < hdr_end && *line_end != '\r') line_end++;

            char *sp1 = (char *)memchr(base, ' ', (size_t)(line_end - base));
            if (!sp1) { consumed += (size_t)(hdr_end - base) + 4; continue; }

            size_t mlen = (size_t)(sp1 - base);
            if (mlen >= sizeof method_buf) mlen = sizeof method_buf - 1;
            memcpy(method_buf, base, mlen);

            char *path_start = sp1 + 1;
            char *sp2 = (char *)memchr(path_start, ' ',
                                        (size_t)(line_end - path_start));
            if (!sp2) sp2 = line_end;

            char *qm = (char *)memchr(path_start, '?',
                                       (size_t)(sp2 - path_start));
            if (qm) {
                size_t plen = (size_t)(qm - path_start);
                size_t qlen = (size_t)(sp2 - qm - 1);
                if (plen >= sizeof path_buf)  plen = sizeof path_buf  - 1;
                if (qlen >= sizeof query_buf) qlen = sizeof query_buf - 1;
                memcpy(path_buf,  path_start, plen);
                memcpy(query_buf, qm + 1,     qlen);
            } else {
                size_t plen = (size_t)(sp2 - path_start);
                if (plen >= sizeof path_buf) plen = sizeof path_buf - 1;
                memcpy(path_buf, path_start, plen);
            }

            /* ---- Content-Length header ---- */
            long content_length = 0;
            char *hl = base;
            while (hl < hdr_end) {
                char *he = hl;
                while (he < hdr_end && *he != '\r') he++;
                if ((size_t)(he - hl) > 15) {
                    /* case-insensitive compare first 15 chars */
                    char tmp[16]; size_t cl = (size_t)(he - hl) < 15 ? (size_t)(he-hl) : 15;
                    for (size_t k=0;k<cl;k++) tmp[k]=(char)(hl[k]|0x20); tmp[cl]=0;
                    if (memcmp(tmp, "content-length:", 15) == 0)
                        content_length = strtol(hl + 15, NULL, 10);
                }
                hl = he + 2; /* skip \r\n */
            }

            /* ---- Body ---- */
            char *body_start = hdr_end + 4;
            size_t body_available = (size_t)(rbuf + rlen - body_start);
            char *body_buf = NULL;
            const char *body_str = "";
            if (content_length > 0) {
                if ((size_t)content_length > body_available) break; /* need more */
                body_buf = (char *)malloc((size_t)content_length + 1);
                if (body_buf) {
                    memcpy(body_buf, body_start, (size_t)content_length);
                    body_buf[content_length] = '\0';
                    body_str = body_buf;
                }
            }

            /* ---- Dispatch ---- */
            Lm_Request req;
            req.method = method_buf;
            req.path   = path_buf;
            req.query  = query_buf;
            req.body   = body_str;
            Lm_Response resp = handler(req, NULL);
            /* Do NOT free body_buf yet — resp.body may alias it. */

            /* ---- Format response ---- */
            const char *rbody = resp.body         ? resp.body         : "";
            const char *ctype = resp.content_type ? resp.content_type
                                                  : "text/plain; charset=utf-8";
            size_t blen  = strlen(rbody);
            size_t ctlen = strlen(ctype);
            const char *stext = Lm__status_text(resp.status);

            /* Header: "HTTP/1.1 %lld %s\r\nContent-Type: %s\r\n
                        Content-Length: %zu\r\nConnection: keep-alive\r\n\r\n" */
            size_t need = wlen + 128 + ctlen + blen;
            if (need > wcap) {
                while (need > wcap) wcap *= 2;
                char *nw = (char *)realloc(wbuf, wcap);
                if (!nw) { if (body_buf) free(body_buf); goto req_done; }
                wbuf = nw;
            }
            int hlen = snprintf(wbuf + wlen, wcap - wlen,
                "HTTP/1.1 %lld %s\r\n"
                "Content-Type: %s\r\n"
                "Content-Length: %zu\r\n"
                "Connection: keep-alive\r\n\r\n",
                (long long)resp.status, stext, ctype, blen);
            if (hlen <= 0) { if (body_buf) free(body_buf); goto req_done; }
            wlen += (size_t)hlen;
            if (blen) {
                memcpy(wbuf + wlen, rbody, blen);
                wlen += blen;
            }

            /* Now safe to release the body buffer. */
            if (body_buf) free(body_buf);

            consumed += (size_t)(body_start - base) +
                        (content_length > 0 ? (size_t)content_length : 0);
        }

        /* Shift unprocessed bytes to front of rbuf. */
        if (consumed > 0 && consumed < rlen) {
            memmove(rbuf, rbuf + consumed, rlen - consumed);
        }
        rlen = (consumed < rlen) ? rlen - consumed : 0;

        /* Flush all coalesced responses in one write pass. */
        if (wlen > 0) {
            size_t off = 0;
            while (off < wlen) {
                ssize_t w = write(fd, wbuf + off, wlen - off);
                if (w < 0) { if (errno == EINTR) continue; goto req_done; }
                off += (size_t)w;
            }
        }
    }
req_done:
    free(wbuf);
    close(fd);
    return NULL;
}

static inline void Lm_http_serve_req(const char *host, int64_t port,
                                     Lm_Closure handler_clos) {
    Lm_ReqHandlerFn handler = (Lm_ReqHandlerFn)handler_clos.fn;

    int s = socket(AF_INET, SOCK_STREAM, 0);
    if (s < 0) { perror("socket"); exit(1); }
    int one = 1;
    setsockopt(s, SOL_SOCKET, SO_REUSEADDR, &one, sizeof one);

    struct sockaddr_in addr;
    memset(&addr, 0, sizeof addr);
    addr.sin_family = AF_INET;
    addr.sin_port   = htons((uint16_t)port);
    inet_pton(AF_INET, host, &addr.sin_addr);
    if (bind(s, (struct sockaddr *)&addr, sizeof addr) < 0) { perror("bind"); exit(1); }
    if (listen(s, 1024) < 0) { perror("listen"); exit(1); }

    fprintf(stderr, "lumen: listening on %s:%" PRId64 " (router mode)\n", host, port);

    for (;;) {
        int fd = accept(s, NULL, NULL);
        if (fd < 0) { if (errno == EINTR) continue; perror("accept"); break; }
        int nd = 1;
        setsockopt(fd, IPPROTO_TCP, TCP_NODELAY, &nd, sizeof nd);
        Lm_http_req_conn *c = (Lm_http_req_conn *)malloc(sizeof *c);
        c->fd      = fd;
        c->handler = handler;
        pthread_t t;
        if (pthread_create(&t, NULL, Lm_http_req_handle, c) != 0) {
            close(fd);
            free(c);
            continue;
        }
        pthread_detach(t);
    }
}

#endif
