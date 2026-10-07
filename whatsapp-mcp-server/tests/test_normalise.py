import pytest

from whatsapp import _match_rank, _query_words, normalise_text


@pytest.mark.parametrize(
    "text, expected",
    [
        ("Rubén García", "ruben garcia"),
        ("Begoña", "begona"),
        ("MAAYAN", "maayan"),
        ("Straße", "strasse"),
        ("  José   Ramírez ", "jose ramirez"),
        ("", ""),
        (None, ""),
    ],
)
def test_normalise_text(text, expected):
    assert normalise_text(text) == expected


def test_query_words_are_normalised():
    assert _query_words("Garcia RUBÉN") == ["garcia", "ruben"]


def test_phone_query_collapses_to_digits():
    assert _query_words("+52 1 55 1234 5678") == ["5215512345678"]
    assert _query_words("(55) 1234-5678") == ["5512345678"]


def test_blank_query_has_no_words():
    assert _query_words("   ") == []
    assert _query_words(None) == []


def test_any_word_order_matches():
    haystack = normalise_text("Rubén García")
    assert _match_rank(_query_words("garcia ruben"), haystack) == 0
    assert _match_rank(_query_words("ruben garcia"), haystack) == 0


def test_partial_words_match():
    assert _match_rank(_query_words("rub garc"), normalise_text("Rubén García")) == 0


def test_every_word_must_match():
    assert _match_rank(_query_words("ruben torres"), normalise_text("Rubén García")) is None


def test_inside_word_match_ranks_lower():
    assert _match_rank(["ana"], normalise_text("Ana López")) == 0
    assert _match_rank(["ana"], normalise_text("Mariana Ruiz")) == 1


def test_no_words_never_match():
    assert _match_rank([], "anything") is None
