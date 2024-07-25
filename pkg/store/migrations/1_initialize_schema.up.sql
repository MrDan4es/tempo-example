CREATE TABLE users
(
    id          SERIAL,

    username    VARCHAR(255) NOT NULL DEFAULT '',

    password    TEXT,

    create_time TIMESTAMPTZ  NOT NULL DEFAULT now()
);
