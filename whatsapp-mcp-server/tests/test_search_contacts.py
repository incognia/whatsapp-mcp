import os

import whatsapp
from whatsapp import search_contacts

RUBEN_GARCIA = "5215512345678@s.whatsapp.net"
RUBEN_TORRES = "5215522222222@s.whatsapp.net"


def jids(results):
    return [c.jid for c in results]


def names(results):
    return [c.name for c in results]


# Accent- and case-insensitive name matching

def test_unaccented_query_finds_accented_name(stores):
    assert "Rubén García" in names(search_contacts("ruben"))


def test_accented_query_finds_unaccented_name(stores):
    assert "Jose Ramirez" in names(search_contacts("José"))


def test_case_is_ignored(stores):
    assert "Noa Peretz" in names(search_contacts("NOA"))


def test_tilde_on_n_is_ignored(stores):
    assert "Begoña Peña" in names(search_contacts("begona pena"))


# Multi-word queries match in any order

def test_reversed_word_order(stores):
    assert RUBEN_GARCIA in jids(search_contacts("garcia ruben"))


def test_partial_words(stores):
    assert RUBEN_GARCIA in jids(search_contacts("rub garc"))


def test_every_word_must_match(stores):
    results = jids(search_contacts("ruben torres"))
    assert RUBEN_TORRES in results
    assert RUBEN_GARCIA not in results


def test_words_spread_across_name_fields(stores):
    # "Lupita" is the saved first name, "Hernández" only appears in the profile name
    results = search_contacts("hernandez lupita")
    assert "5215577777777@s.whatsapp.net" in jids(results)


# Phone number matching

def test_partial_phone_number(stores):
    assert RUBEN_GARCIA in jids(search_contacts("5512345"))


def test_formatted_phone_number(stores):
    assert RUBEN_GARCIA in jids(search_contacts("+52 1 55 1234 5678"))


# Address-book contacts are searchable

def test_contact_without_message_history(stores):
    results = search_contacts("begona")
    assert [(c.name, c.phone_number, c.jid) for c in results] == [
        ("Begoña Peña", "5215566666666", "5215566666666@s.whatsapp.net")
    ]


def test_chat_named_only_by_number_gets_address_book_name(stores):
    results = search_contacts("ruben")
    ruben = next(c for c in results if c.jid == RUBEN_GARCIA)
    assert ruben.name == "Rubén García"


def test_business_name_is_searchable(stores):
    assert "Tacos Doña Chela" in names(search_contacts("tacos dona"))


def test_contact_store_unavailable_falls_back_to_chats(stores, monkeypatch, tmp_path):
    monkeypatch.setattr(whatsapp, "WHATSMEOW_DB_PATH", str(tmp_path / "missing.db"))
    assert names(search_contacts("jose")) == ["Jose Ramirez"]
    # Address-book-only contacts are simply not found
    assert search_contacts("begona") == []


# LID-keyed contacts resolve to phone numbers

def test_lid_contact_reported_by_phone_number(stores):
    for contact in search_contacts("ruben"):
        assert not contact.jid.endswith("@lid")
        assert contact.jid == f"{contact.phone_number}@s.whatsapp.net"


def test_unmapped_lid_contact_is_omitted(stores):
    assert search_contacts("fantasma") == []


# One result per contact

def test_same_person_in_several_sources_appears_once(stores):
    assert jids(search_contacts("ruben garcia")).count(RUBEN_GARCIA) == 1


def test_saved_name_preferred_over_profile_name(stores):
    ruben = next(c for c in search_contacts("garcia") if c.jid == RUBEN_GARCIA)
    assert ruben.name == "Rubén García"


def test_profile_name_remains_searchable(stores):
    results = search_contacts("rubo")
    assert [(c.jid, c.name) for c in results] == [(RUBEN_GARCIA, "Rubén García")]


# Groups excluded from contact search

def test_group_with_matching_name_is_excluded(stores):
    results = search_contacts("familia garcia")
    assert results == []
    assert all(not c.jid.endswith("@g.us") for c in search_contacts("garcia"))


def test_status_broadcast_is_excluded(stores):
    assert search_contacts("status") == []


# Result shape, limit and ordering

def test_result_limit(stores):
    assert len(search_contacts("masivo")) == 50


def test_word_start_matches_first(stores):
    assert names(search_contacts("ana"))[:2] == ["Ana López", "Mariana Ruiz"]


def test_contacts_with_a_chat_come_first(stores):
    # Both start with "ruben"; Torres and García have chats, so they lead
    results = search_contacts("ruben")
    assert set(jids(results)[:2]) == {RUBEN_GARCIA, RUBEN_TORRES}


def test_stable_order(stores):
    assert search_contacts("a") == search_contacts("a")


def test_blank_query_returns_empty_list(stores):
    assert search_contacts("   ") == []


def test_result_fields_unchanged(stores):
    contact = search_contacts("noa")[0]
    assert vars(contact) == {
        "phone_number": "9725550000001",
        "name": "Noa Peretz",
        "jid": "9725550000001@s.whatsapp.net",
    }


# Read-only access to the contact store

def test_searching_does_not_change_the_contact_store(stores):
    path = stores["whatsmeow"]
    before = (path.read_bytes(), os.stat(path).st_mtime_ns)
    for query in ("ruben", "noa", "5512345", "masivo", "   "):
        search_contacts(query)
    assert (path.read_bytes(), os.stat(path).st_mtime_ns) == before
